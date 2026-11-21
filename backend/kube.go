package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	appslisters "k8s.io/client-go/listers/apps/v1"
	batchlisters "k8s.io/client-go/listers/batch/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
)

const (
	lblManaged = "app.kubernetes.io/managed-by"
	lblServer  = "kubepilot.io/server"
	lblRole    = "kubepilot.io/role"
	lblOwner   = "kubepilot.io/owner" // on server pods: servers of the same owner may talk to each other
	managedBy  = "kubepilot"
	mainCtr    = "server"
	installCtr = "install"
)

var ErrNoVolume = errors.New("The server's volume doesn't exist yet (installation in progress?)")

func stsName(id string) string { return "srv-" + id }
func pvcName(id string) string { return "srv-" + id + "-data" }
func podName(id string) string { return "srv-" + id + "-0" }

// PodIP returns the cluster IP of a server's running pod ("" if none).
func (k *Kube) PodIP(id string) string {
	p, err := k.pods.Pods(k.ns).Get(podName(id))
	if err != nil || p.Status.Phase != corev1.PodRunning {
		return ""
	}
	return p.Status.PodIP
}

func srvLabels(id, role string) map[string]string {
	return map[string]string{lblManaged: managedBy, lblServer: id, lblRole: role}
}

type Usage struct {
	CPU float64 // millicores
	Mem int64   // bytes
}

type Kube struct {
	cs           *kubernetes.Clientset
	rc           *rest.Config
	ns           string
	storageRoot  string
	storageClass string
	rootInstall  bool        // the namespace accepts root install containers (eggs)
	ingress      IngressPeer // the ingress controller game servers may reach

	pods corelisters.PodLister
	sts  appslisters.StatefulSetLister
	jobs batchlisters.JobLister

	nodeName  string    // the panel's node: its volumes are mounted, the others' are reached through their agent
	nodes     *nodePool // nil: node agents turned off
	nodeCache nodeCache

	mu      sync.RWMutex
	metrics map[string]Usage
	vols    map[string]VolRef

	attachMu sync.Mutex
	attach   map[string]*attachSess
}

func newKube(cfg Config) (*Kube, error) {
	var rc *rest.Config
	var err error
	if cfg.Kubeconfig != "" {
		rc, err = clientcmd.BuildConfigFromFlags("", cfg.Kubeconfig)
	} else {
		rc, err = rest.InClusterConfig()
	}
	if err != nil {
		return nil, err
	}
	rc.QPS, rc.Burst = 50, 100
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, err
	}
	k := &Kube{
		cs: cs, rc: rc, ns: cfg.Namespace, storageRoot: filepath.Clean(cfg.StorageRoot), storageClass: cfg.StorageClass, rootInstall: cfg.RootInstall, ingress: cfg.Ingress,
		nodeName: cfg.NodeName, metrics: map[string]Usage{}, vols: map[string]VolRef{}, attach: map[string]*attachSess{},
	}
	if cfg.NodeAgents && cfg.AgentNamespace != "" {
		if k.nodes, err = newNodePool(k, cfg.AgentNamespace, cfg.AgentSelector, nodeTokenSource()); err != nil {
			return nil, err
		}
	}
	return k, nil
}

func (k *Kube) start(ctx context.Context) error {
	if err := k.ensureNetworkPolicy(ctx); err != nil {
		log.Printf("networkpolicy: %v", err)
	}
	f := informers.NewSharedInformerFactoryWithOptions(k.cs, 10*time.Minute,
		informers.WithNamespace(k.ns),
		informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = lblManaged + "=" + managedBy }))
	k.pods = f.Core().V1().Pods().Lister()
	k.sts = f.Apps().V1().StatefulSets().Lister()
	k.jobs = f.Batch().V1().Jobs().Lister()
	f.Start(ctx.Done())
	for typ, ok := range f.WaitForCacheSync(ctx.Done()) {
		if !ok {
			return fmt.Errorf("cache %v not synced", typ)
		}
	}
	go k.metricsLoop(ctx)
	return nil
}

// ---------- Network isolation for tenant pods

func ownerLabels(l map[string]string, owner int64) map[string]string {
	l[lblOwner] = strconv.FormatInt(owner, 10)
	return l
}

// EnsureOwnerNetwork lets the servers of one owner reach each other (a bot and its Lavalink, a game and its
// database…) through their internal address, while they stay cut off from everybody else's servers and from
// the cluster. Network policies add up: this one only opens traffic between pods of the same owner.
func (k *Kube) EnsureOwnerNetwork(ctx context.Context, owner int64) error {
	id := strconv.FormatInt(owner, 10)
	sel := metav1.LabelSelector{MatchLabels: map[string]string{lblManaged: managedBy, lblOwner: id}}
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "kubepilot-owner-" + id, Namespace: k.ns, Labels: map[string]string{lblManaged: managedBy}},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: sel,
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeEgress},
			Egress:      []networkingv1.NetworkPolicyEgressRule{{To: []networkingv1.NetworkPolicyPeer{{PodSelector: &sel}}}},
		},
	}
	api := k.cs.NetworkingV1().NetworkPolicies(k.ns)
	cur, err := api.Get(ctx, np.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, np, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	cur.Spec = np.Spec
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// InternalAddress is how a server is reached by its owner's other servers.
func (k *Kube) InternalAddress(id string, port int) string {
	return fmt.Sprintf("%s.%s.svc.cluster.local:%d", stsName(id), k.ns, port)
}

// IngressPeer is the cluster's ingress controller, as game servers may reach it. A server that calls a website of
// this same cluster through its public address (a bot fetching its own dashboard) is sent by kube-proxy straight to
// the controller's pods: without this exception, the isolation below refuses that connection. Only these ports of
// these pods open up: the websites, which anyone on the internet reaches anyway — not the controller's dashboard.
type IngressPeer struct {
	Namespace string
	Labels    map[string]string
	Ports     []intstr.IntOrString
}

// parseIngressPeer reads the chart's settings: "kube-system", "app.kubernetes.io/name=traefik", "web,websecure".
// No namespace = no access. Labels and ports are required: an empty selector would open every pod of the namespace.
func parseIngressPeer(ns, sel, ports string) (IngressPeer, error) {
	if ns = strings.TrimSpace(ns); ns == "" {
		return IngressPeer{}, nil
	}
	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return IngressPeer{}, fmt.Errorf("namespace %q: %s", ns, strings.Join(errs, ", "))
	}
	lbls, err := labels.ConvertSelectorToLabelsMap(sel)
	if err != nil {
		return IngressPeer{}, fmt.Errorf("pod labels %q: %w", sel, err)
	}
	if len(lbls) == 0 {
		return IngressPeer{}, fmt.Errorf("pod labels are required (an empty selector would match every pod of the namespace)")
	}
	ing := IngressPeer{Namespace: ns, Labels: lbls}
	for _, p := range strings.Split(ports, ",") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		if n, err := strconv.Atoi(p); err == nil {
			if n < 1 || n > 65535 {
				return IngressPeer{}, fmt.Errorf("port %d: out of range", n)
			}
			ing.Ports = append(ing.Ports, intstr.FromInt32(int32(n)))
		} else if errs := validation.IsValidPortName(p); len(errs) > 0 {
			return IngressPeer{}, fmt.Errorf("port %q: %s", p, strings.Join(errs, ", "))
		} else {
			ing.Ports = append(ing.Ports, intstr.FromString(p))
		}
	}
	if len(ing.Ports) == 0 {
		return IngressPeer{}, fmt.Errorf("ports are required (none would open every port of the controller)")
	}
	return ing, nil
}

func (k *Kube) ensureNetworkPolicy(ctx context.Context) error {
	var nodeIPs []string
	if nodes, err := k.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		for _, n := range nodes.Items {
			for _, a := range n.Status.Addresses {
				if a.Type == corev1.NodeInternalIP || a.Type == corev1.NodeExternalIP {
					nodeIPs = append(nodeIPs, a.Address)
				}
			}
		}
	}
	np := isolationPolicy(k.ns, nodeIPs, k.ingress)
	api := k.cs.NetworkingV1().NetworkPolicies(k.ns)
	cur, err := api.Get(ctx, np.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, np, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	cur.Spec = np.Spec
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// isolationPolicy: game servers reach the internet, the cluster's DNS and the servers' databases — not the other
// pods of the cluster, its nodes, or private networks. (Their owner's other servers: kubepilot-owner-<id> policies.)
func isolationPolicy(ns string, nodeIPs []string, ing IngressPeer) *networkingv1.NetworkPolicy {
	except := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "100.64.0.0/10", "169.254.0.0/16"}
	seen := map[string]bool{}
	for _, a := range nodeIPs {
		ip, err := netip.ParseAddr(a)
		if err != nil || !ip.Is4() || seen[ip.String()] || isPrivateV4(ip.String()) {
			continue // IPv6: the pods have none
		}
		seen[ip.String()] = true
		except = append(except, ip.String()+"/32")
	}
	udp, tcp := corev1.ProtocolUDP, corev1.ProtocolTCP
	dns, mysqlPort := intstr.FromInt32(53), intstr.FromInt32(3306)
	egress := []networkingv1.NetworkPolicyEgressRule{
		{To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: except}}}},
		{
			To: []networkingv1.NetworkPolicyPeer{{
				NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}},
				PodSelector:       &metav1.LabelSelector{MatchLabels: map[string]string{"k8s-app": "kube-dns"}},
			}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &udp, Port: &dns}, {Protocol: &tcp, Port: &dns}},
		},
		{ // the servers' databases (MariaDB deployed by the chart), and nothing else of the cluster
			To:    []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"app.kubernetes.io/name": "kubepilot-mariadb"}}}},
			Ports: []networkingv1.NetworkPolicyPort{{Protocol: &tcp, Port: &mysqlPort}},
		},
	}
	if ing.Namespace != "" && len(ing.Labels) > 0 && len(ing.Ports) > 0 {
		rule := networkingv1.NetworkPolicyEgressRule{To: []networkingv1.NetworkPolicyPeer{{
			NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": ing.Namespace}},
			PodSelector:       &metav1.LabelSelector{MatchLabels: ing.Labels},
		}}}
		for i := range ing.Ports {
			rule.Ports = append(rule.Ports, networkingv1.NetworkPolicyPort{Protocol: &tcp, Port: &ing.Ports[i]})
		}
		egress = append(egress, rule)
	}
	return &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "kubepilot-isolation", Namespace: ns, Labels: map[string]string{lblManaged: managedBy}},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{lblManaged: managedBy}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{{}},
			Egress:      egress,
		},
	}
}

func isPrivateV4(ip string) bool {
	return strings.HasPrefix(ip, "10.") || strings.HasPrefix(ip, "192.168.") || strings.HasPrefix(ip, "100.64.") ||
		(strings.HasPrefix(ip, "172.") && func() bool {
			p := strings.Split(ip, ".")
			n, _ := strconv.Atoi(p[1])
			return n >= 16 && n <= 31
		}())
}

// ---------- Manifests

type Spec struct {
	Server   *Server
	Template *Template
	Allocs   []*Allocation
	Settings Settings
	Routed   string // hostname given to the Minecraft router ("" = not routed)
	Wakeable bool   // asleep (auto-sleep): mc-router starts it when a player connects
}

func escapeDollar(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// EffectiveVars returns the value of each template variable for this server.
func EffectiveVars(s *Server, t *Template) map[string]string {
	out := map[string]string{}
	for _, v := range t.Vars {
		out[v.Key] = v.Default
		if val, ok := s.Env[v.Key]; ok {
			out[v.Key] = val
		}
	}
	return out
}

func (sp Spec) env() []corev1.EnvVar {
	s, t := sp.Server, sp.Template
	port := 0
	for _, a := range sp.Allocs {
		if a.IsPrimary {
			port = a.TargetPort
		}
	}
	// Java needs room outside the heap (metaspace, threads, direct buffers): keep max(640 MB, 20 %) free.
	overhead := s.MemoryMB / 5
	if overhead < 640 {
		overhead = 640
	}
	jvm := s.MemoryMB - overhead
	if jvm < s.MemoryMB/2 {
		jvm = s.MemoryMB / 2
	}
	list := []corev1.EnvVar{
		{Name: "KUBEPILOT_SERVER_ID", Value: s.ID},
		{Name: "SERVER_MEMORY", Value: strconv.Itoa(s.MemoryMB)},
		{Name: "SERVER_JVM_MEMORY", Value: strconv.Itoa(jvm)},
		{Name: "SERVER_PORT", Value: strconv.Itoa(port)},
		{Name: "SERVER_IP", Value: "0.0.0.0"},
		{Name: "TZ", Value: sp.Settings.Timezone},
		{Name: "HOME", Value: t.DataPath},
	}
	if t.StartupEnv && strings.TrimSpace(s.Startup) != "" { // what Wings gives Pterodactyl images
		list = append(list, corev1.EnvVar{Name: "STARTUP", Value: escapeDollar(s.Startup)}, corev1.EnvVar{Name: "P_SERVER_UUID", Value: s.ID})
	}
	idx := map[string]int{}
	for i, e := range list {
		idx[e.Name] = i
	}
	for _, v := range t.Vars {
		val := v.Default
		if custom, ok := s.Env[v.Key]; ok {
			val = escapeDollar(custom) // user values are literal
		}
		if i, ok := idx[v.Key]; ok {
			list[i].Value = val
			continue
		}
		idx[v.Key] = len(list)
		list = append(list, corev1.EnvVar{Name: v.Key, Value: val})
	}
	return list
}

func mi(mb int) resource.Quantity {
	return *resource.NewQuantity(int64(mb)*1024*1024, resource.BinarySI)
}
func milli(m int) resource.Quantity { return *resource.NewMilliQuantity(int64(m), resource.DecimalSI) }

func (sp Spec) resources() corev1.ResourceRequirements {
	s := sp.Server
	limMB := s.MemoryMB
	if strings.HasPrefix(sp.Template.Source, "pterodactyl-egg") {
		limMB += eggOverhead(s.MemoryMB)
	}
	lim := corev1.ResourceList{corev1.ResourceMemory: mi(limMB)}
	reqMem := s.MemoryMB / 2
	if reqMem < 64 {
		reqMem = 64
	}
	req := corev1.ResourceList{corev1.ResourceMemory: mi(reqMem), corev1.ResourceCPU: milli(100)}
	if s.CPUMillis > 0 {
		lim[corev1.ResourceCPU] = milli(s.CPUMillis)
		c := s.CPUMillis / 10
		if c < 50 {
			c = 50
		}
		req[corev1.ResourceCPU] = milli(c)
	}
	return corev1.ResourceRequirements{Limits: lim, Requests: req}
}

func boolp(b bool) *bool { return &b }

func (sp Spec) podSecurity() (*corev1.PodSecurityContext, *corev1.SecurityContext) {
	uid := sp.Template.RunAsUser
	return &corev1.PodSecurityContext{
			RunAsUser: &uid, RunAsGroup: &uid, RunAsNonRoot: boolp(true),
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		}, &corev1.SecurityContext{
			AllowPrivilegeEscalation: boolp(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
		}
}

func protocols(p string) []corev1.Protocol {
	switch p {
	case "udp":
		return []corev1.Protocol{corev1.ProtocolUDP}
	case "tcp+udp":
		return []corev1.Protocol{corev1.ProtocolTCP, corev1.ProtocolUDP}
	}
	return []corev1.Protocol{corev1.ProtocolTCP}
}

func (sp Spec) podTemplate() corev1.PodTemplateSpec {
	s, t := sp.Server, sp.Template
	psc, csc := sp.podSecurity()
	grace := int64(t.StopGrace)
	c := corev1.Container{
		Name:            mainCtr,
		Image:           s.Image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		WorkingDir:      t.DataPath,
		Env:             sp.env(),
		Stdin:           true,
		StdinOnce:       false,
		TTY:             false,
		Resources:       sp.resources(),
		SecurityContext: csc,
		VolumeMounts:    []corev1.VolumeMount{{Name: "data", MountPath: t.DataPath}, {Name: "tmp", MountPath: "/tmp"}},
	}
	if strings.TrimSpace(s.Startup) != "" && !t.StartupEnv {
		c.Command = []string{"/bin/sh", "-c", escapeDollar(s.Startup)}
	}
	if t.StopCommand != "" {
		// Before any stop (button, restart, node drain), type the stop command in the game's console and wait
		// for it to exit, so it saves cleanly; the grace period still bounds the wait.
		c.Lifecycle = &corev1.Lifecycle{PreStop: &corev1.LifecycleHandler{Exec: &corev1.ExecAction{
			Command: []string{"/bin/sh", "-c", stopScript(t.StopCommand)},
		}}}
	}
	seen := map[string]bool{}
	var probePort int
	for _, a := range sp.Allocs {
		for _, proto := range protocols(a.Protocol) {
			key := fmt.Sprintf("%d/%s", a.TargetPort, proto)
			if seen[key] {
				continue
			}
			seen[key] = true
			c.Ports = append(c.Ports, corev1.ContainerPort{ContainerPort: int32(a.TargetPort), Protocol: proto})
			if a.IsPrimary && proto == corev1.ProtocolTCP {
				probePort = a.TargetPort
			}
		}
	}
	if t.ReadyProbe && probePort > 0 {
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt(probePort)}},
			InitialDelaySeconds: 5, PeriodSeconds: 5, TimeoutSeconds: 3, FailureThreshold: 3,
		}
	}
	tmpLimit := resource.MustParse("1Gi")
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{Labels: ownerLabels(srvLabels(s.ID, "server"), s.OwnerID)},
		Spec: corev1.PodSpec{
			AutomountServiceAccountToken:  boolp(false),
			EnableServiceLinks:            boolp(false),
			Affinity:                      onNode(s.Node),
			TerminationGracePeriodSeconds: &grace,
			SecurityContext:               psc,
			Containers:                    []corev1.Container{c},
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName(s.ID)}}},
				{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &tmpLimit}}},
			},
		},
	}
}

// onNode pins a pod to the node its server was placed on.
func onNode(node string) *corev1.Affinity {
	if node == "" {
		return nil
	}
	return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchFields: []corev1.NodeSelectorRequirement{
			{Key: "metadata.name", Operator: corev1.NodeSelectorOpIn, Values: []string{node}},
		}}},
	}}}
}

func (k *Kube) EnsurePVC(ctx context.Context, s *Server) error {
	api := k.cs.CoreV1().PersistentVolumeClaims(k.ns)
	if _, err := api.Get(ctx, pvcName(s.ID), metav1.GetOptions{}); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	sc := k.storageClass
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: pvcName(s.ID), Labels: srvLabels(s.ID, "data")},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: &sc,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: mi(s.DiskMB)}},
		},
	}
	_, err := api.Create(ctx, pvc, metav1.CreateOptions{})
	return err
}

func (k *Kube) EnsureService(ctx context.Context, sp Spec) error {
	api := k.cs.CoreV1().Services(k.ns)
	name := stsName(sp.Server.ID)
	var ports []corev1.ServicePort
	for _, a := range sp.Allocs {
		for _, proto := range protocols(a.Protocol) {
			name := fmt.Sprintf("%s-%d", strings.ToLower(string(proto)), a.Port)
			if sp.Routed != "" && a.IsPrimary && proto == corev1.ProtocolTCP {
				name = "mc-router" // the port mc-router connects to
			}
			sport := corev1.ServicePort{Name: name, Protocol: proto, Port: int32(a.Port), TargetPort: intstr.FromInt(a.TargetPort)}
			if !sp.Server.InternalOnly {
				sport.NodePort = int32(a.Port)
			}
			ports = append(ports, sport)
		}
	}
	cur, err := api.Get(ctx, name, metav1.GetOptions{})
	if len(ports) == 0 {
		if err == nil {
			return api.Delete(ctx, name, metav1.DeleteOptions{})
		}
		return nil
	}
	spec := corev1.ServiceSpec{
		Type:                  corev1.ServiceTypeNodePort,
		Selector:              map[string]string{lblServer: sp.Server.ID, lblRole: "server"},
		Ports:                 ports,
		ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyLocal,
	}
	if sp.Server.InternalOnly { // reachable through its ClusterIP only (the owner's other servers)
		spec.Type, spec.ExternalTrafficPolicy = corev1.ServiceTypeClusterIP, ""
	}
	annotations := routerAnnotations(sp)
	if apierrors.IsNotFound(err) {
		_, err = api.Create(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: srvLabels(sp.Server.ID, "service"), Annotations: annotations}, Spec: spec}, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}
	for _, k := range routerAnnotationKeys {
		delete(cur.Annotations, k)
	}
	for k, v := range annotations {
		cur.Annotations[k] = v
	}
	cur.Spec.Ports, cur.Spec.Selector, cur.Spec.Type, cur.Spec.ExternalTrafficPolicy = spec.Ports, spec.Selector, spec.Type, spec.ExternalTrafficPolicy
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// ApplyStatefulSet writes the latest pod spec and sets replicas.
func (k *Kube) ApplyStatefulSet(ctx context.Context, sp Spec, replicas int32) error {
	api := k.cs.AppsV1().StatefulSets(k.ns)
	name := stsName(sp.Server.ID)
	tpl := sp.podTemplate()
	cur, err := api.Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		sts := &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: name, Labels: srvLabels(sp.Server.ID, "server"), Annotations: map[string]string{specHashAnnotation: sp.hash()}},
			Spec: appsv1.StatefulSetSpec{
				Replicas:    &replicas,
				ServiceName: name,
				Selector:    &metav1.LabelSelector{MatchLabels: map[string]string{lblServer: sp.Server.ID, lblRole: "server"}},
				Template:    tpl,
			},
		}
		_, err = api.Create(ctx, sts, metav1.CreateOptions{})
		return err
	} else if err != nil {
		return err
	}
	cur.Spec.Template = tpl
	cur.Spec.Replicas = &replicas
	if cur.Annotations == nil {
		cur.Annotations = map[string]string{}
	}
	cur.Annotations[specHashAnnotation] = sp.hash()
	_, err = api.Update(ctx, cur, metav1.UpdateOptions{})
	return err
}

// Replicas is the StatefulSet's desired replica count (-1: no StatefulSet).
func (k *Kube) Replicas(id string) int32 {
	sts, err := k.sts.StatefulSets(k.ns).Get(stsName(id))
	if err != nil {
		return -1
	}
	if sts.Spec.Replicas == nil {
		return 1
	}
	return *sts.Spec.Replicas
}

// SpecHash is the hash of the spec the StatefulSet was last written with (see Spec.hash).
func (k *Kube) SpecHash(id string) string {
	sts, err := k.sts.StatefulSets(k.ns).Get(stsName(id))
	if err != nil {
		return ""
	}
	return sts.Annotations[specHashAnnotation]
}

func (k *Kube) SetReplicas(ctx context.Context, id string, n int32) error {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, n)
	_, err := k.cs.AppsV1().StatefulSets(k.ns).Patch(ctx, stsName(id), types.MergePatchType, []byte(patch), metav1.PatchOptions{})
	if apierrors.IsNotFound(err) && n == 0 {
		return nil
	}
	return err
}

func (k *Kube) KillPod(ctx context.Context, id string) error {
	zero := int64(0)
	err := k.cs.CoreV1().Pods(k.ns).Delete(ctx, podName(id), metav1.DeleteOptions{GracePeriodSeconds: &zero})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func (k *Kube) PodExists(id string) bool {
	_, err := k.pods.Pods(k.ns).Get(podName(id))
	return err == nil
}

// RunInstall launches the install job, which also binds the volume.
func (k *Kube) RunInstall(ctx context.Context, sp Spec) error {
	s, t := sp.Server, sp.Template
	bg := metav1.DeletePropagationBackground
	sel := labels.SelectorFromSet(map[string]string{lblServer: s.ID, lblRole: "install"}).String()
	_ = k.cs.BatchV1().Jobs(k.ns).DeleteCollection(ctx, metav1.DeleteOptions{PropagationPolicy: &bg}, metav1.ListOptions{LabelSelector: sel})

	psc, csc := sp.podSecurity()
	script := strings.ReplaceAll(t.InstallScript, "\r\n", "\n")
	if strings.TrimSpace(script) == "" {
		script = "echo 'Volume ready.'"
	}
	mount, shell := t.DataPath, "/bin/sh"
	if t.InstallPath != "" {
		mount = t.InstallPath
	}
	if t.InstallShell != "" {
		shell = "/bin/" + t.InstallShell
	}
	command := "set -e\n" + script
	if t.InstallAsRoot { // egg scripts expect Wings' behaviour: no "set -e"
		command = script
		if !k.rootInstall {
			command = "echo '[KubePilot] Root installs are disabled on this cluster (serversNamespace.rootInstall=false): the install script runs without root and may fail.'\n" + script
		}
	}
	if t.InstallAsRoot && k.rootInstall {
		// Egg install scripts install packages as root (apt, apk, SteamCMD…): root, but without privilege
		// escalation, seccomp on and only the capabilities package managers need. Whatever happens, the files
		// are handed back to the game's user, which never runs as root.
		uid := t.RunAsUser
		root, nonRoot := int64(0), false
		psc = &corev1.PodSecurityContext{RunAsUser: &root, RunAsGroup: &root, RunAsNonRoot: &nonRoot,
			SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}}
		csc = &corev1.SecurityContext{AllowPrivilegeEscalation: boolp(false), Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"}, Add: []corev1.Capability{"CHOWN", "DAC_OVERRIDE", "FOWNER", "FSETID", "SETUID", "SETGID"}}}
		command = fmt.Sprintf("trap 'chown -R %d:%d %s' EXIT\ncd %s\n%s", uid, uid, mount, mount, script)
	}
	backoff, ttl, deadline := int32(0), int32(6*3600), int64(1800)
	tmpLimit := resource.MustParse("1Gi")
	memMB := s.MemoryMB
	if memMB > 2048 {
		memMB = 2048
	}
	if memMB < 256 {
		memMB = 256
	}
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("srv-%s-install-%d", s.ID, time.Now().Unix()), Labels: srvLabels(s.ID, "install")},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoff, TTLSecondsAfterFinished: &ttl, ActiveDeadlineSeconds: &deadline,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: srvLabels(s.ID, "install")},
				Spec: corev1.PodSpec{
					RestartPolicy:                corev1.RestartPolicyNever,
					AutomountServiceAccountToken: boolp(false),
					Affinity:                     onNode(s.Node), // the volume is created where the install runs first
					EnableServiceLinks:           boolp(false),
					SecurityContext:              psc,
					Containers: []corev1.Container{{
						Name: installCtr, Image: t.InstallImage, ImagePullPolicy: corev1.PullIfNotPresent,
						Command:    []string{shell, "-c", escapeDollar(command)},
						WorkingDir: mount, Env: sp.env(), SecurityContext: csc,
						Resources: corev1.ResourceRequirements{
							Limits:   corev1.ResourceList{corev1.ResourceMemory: mi(memMB), corev1.ResourceCPU: milli(2000)},
							Requests: corev1.ResourceList{corev1.ResourceMemory: mi(128), corev1.ResourceCPU: milli(100)},
						},
						VolumeMounts: []corev1.VolumeMount{{Name: "data", MountPath: mount}, {Name: "tmp", MountPath: "/tmp"}},
					}},
					Volumes: []corev1.Volume{
						{Name: "data", VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: pvcName(s.ID)}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &tmpLimit}}},
					},
				},
			},
		},
	}
	_, err := k.cs.BatchV1().Jobs(k.ns).Create(ctx, job, metav1.CreateOptions{})
	return err
}

func (k *Kube) DeleteServer(ctx context.Context, id string) error {
	bg := metav1.DeletePropagationBackground
	sel := labels.SelectorFromSet(map[string]string{lblServer: id}).String()
	var errs []string
	ignore := func(err error) {
		if err != nil && !apierrors.IsNotFound(err) {
			errs = append(errs, err.Error())
		}
	}
	ignore(k.cs.AppsV1().StatefulSets(k.ns).Delete(ctx, stsName(id), metav1.DeleteOptions{PropagationPolicy: &bg}))
	ignore(k.cs.CoreV1().Services(k.ns).Delete(ctx, stsName(id), metav1.DeleteOptions{}))
	ignore(k.cs.BatchV1().Jobs(k.ns).DeleteCollection(ctx, metav1.DeleteOptions{PropagationPolicy: &bg}, metav1.ListOptions{LabelSelector: sel}))
	zero := int64(0)
	ignore(k.cs.CoreV1().Pods(k.ns).DeleteCollection(ctx, metav1.DeleteOptions{GracePeriodSeconds: &zero}, metav1.ListOptions{LabelSelector: sel}))
	ignore(k.cs.CoreV1().PersistentVolumeClaims(k.ns).Delete(ctx, pvcName(id), metav1.DeleteOptions{}))
	k.mu.Lock()
	delete(k.vols, id)
	k.mu.Unlock()
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// UsedNodePorts returns every NodePort in use in the cluster (all namespaces).
func (k *Kube) UsedNodePorts(ctx context.Context) (map[int]bool, error) {
	svcs, err := k.cs.CoreV1().Services("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	used := map[int]bool{}
	for _, s := range svcs.Items {
		for _, p := range s.Spec.Ports {
			if p.NodePort > 0 {
				used[int(p.NodePort)] = true
			}
		}
	}
	return used, nil
}

// ---------- Volumes (local-path provisioner = a folder on one node)

// VolRef is where a server's volume lives.
type VolRef struct {
	Node string // node holding it ("" when the volume doesn't say: treated as the panel's node)
	Path string // folder on that node
}

func (k *Kube) Volume(ctx context.Context, id string) (VolRef, error) {
	k.mu.RLock()
	ref, ok := k.vols[id]
	k.mu.RUnlock()
	if ok {
		return ref, nil
	}
	pvc, err := k.cs.CoreV1().PersistentVolumeClaims(k.ns).Get(ctx, pvcName(id), metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return ref, ErrNoVolume
		}
		return ref, err
	}
	if pvc.Spec.VolumeName == "" || pvc.Status.Phase != corev1.ClaimBound {
		return ref, ErrNoVolume
	}
	pv, err := k.cs.CoreV1().PersistentVolumes().Get(ctx, pvc.Spec.VolumeName, metav1.GetOptions{})
	if err != nil {
		return ref, err
	}
	var p string
	switch {
	case pv.Spec.HostPath != nil:
		p = pv.Spec.HostPath.Path
	case pv.Spec.Local != nil:
		p = pv.Spec.Local.Path
	default:
		return ref, errors.New("Volume type not supported by the file manager")
	}
	p = filepath.Clean(p)
	if filepath.Dir(p) != k.storageRoot {
		return ref, errCode(500, "Unexpected volume path: %s", p)
	}
	ref = VolRef{Node: pvNode(pv), Path: p}
	k.mu.Lock()
	k.vols[id] = ref
	k.mu.Unlock()
	return ref, nil
}

// pvNode is the node a local volume is bound to (its required node affinity).
func pvNode(pv *corev1.PersistentVolume) string {
	if pv.Spec.NodeAffinity == nil || pv.Spec.NodeAffinity.Required == nil {
		return ""
	}
	for _, t := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
		for _, e := range t.MatchExpressions {
			if e.Key == corev1.LabelHostname && e.Operator == corev1.NodeSelectorOpIn && len(e.Values) == 1 {
				return e.Values[0]
			}
		}
		for _, f := range t.MatchFields {
			if f.Key == "metadata.name" && f.Operator == corev1.NodeSelectorOpIn && len(f.Values) == 1 {
				return f.Values[0]
			}
		}
	}
	return ""
}

// local: the volumes of this node are mounted in the panel's pod.
func (k *Kube) local(node string) bool { return node == "" || k.nodeName == "" || node == k.nodeName }

// OpenVolume opens a server's volume: directly on the panel's node, through the node's agent elsewhere.
func (k *Kube) OpenVolume(ctx context.Context, id string) (Vol, error) {
	ref, err := k.Volume(ctx, id)
	if err != nil {
		return nil, err
	}
	if k.local(ref.Node) {
		return openLocalVol(ref.Path)
	}
	if k.nodes == nil {
		return nil, errCode(503, "This server is on the node %s and the node agents are turned off (nodeAgent.enabled): its files are out of reach", ref.Node)
	}
	v, err := k.nodes.openVol(ctx, ref.Node, filepath.Base(ref.Path))
	if err != nil {
		log.Printf("node %s: %v", ref.Node, err)
		return nil, errCode(503, "The node %s doesn't answer: the files of this server are out of reach for now", ref.Node)
	}
	return v, nil
}

// VolumeSize adds up the files of a server's volume.
func (k *Kube) VolumeSize(ctx context.Context, id string) (int64, error) {
	ref, err := k.Volume(ctx, id)
	if err != nil {
		return 0, err
	}
	if k.local(ref.Node) {
		return dirSize(ref.Path), nil
	}
	if k.nodes == nil {
		return 0, errNoAgent
	}
	return k.nodes.du(ctx, ref.Node, filepath.Base(ref.Path))
}

// ---------- State

type State struct {
	Status       string  `json:"status"` // offline, installing, starting, running, stopping, crashed
	Reason       string  `json:"reason,omitempty"`
	StartedAt    int64   `json:"startedAt,omitempty"`
	Restarts     int32   `json:"restarts"`
	InstallError bool    `json:"installError,omitempty"`
	CPU          float64 `json:"cpu"`
	Memory       int64   `json:"memory"`
}

var crashReasons = map[string]bool{
	"CrashLoopBackOff": true, "ErrImagePull": true, "ImagePullBackOff": true, "CreateContainerConfigError": true,
	"InvalidImageName": true, "CreateContainerError": true, "RunContainerError": true, "ErrImageNeverPull": true,
}

func (k *Kube) installJob(id string) (active *batchv1.Job, failed bool) {
	jobs, _ := k.jobs.Jobs(k.ns).List(labels.SelectorFromSet(map[string]string{lblServer: id, lblRole: "install"}))
	if len(jobs) == 0 {
		return nil, false
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreationTimestamp.After(jobs[j].CreationTimestamp.Time) })
	last := jobs[0]
	for _, c := range last.Status.Conditions {
		if c.Status == corev1.ConditionTrue {
			if c.Type == batchv1.JobFailed {
				return nil, true
			}
			if c.Type == batchv1.JobComplete {
				return nil, false
			}
		}
	}
	return last, false
}

func (k *Kube) State(id string) State {
	st := State{Status: "offline"}
	k.mu.RLock()
	u := k.metrics[podName(id)]
	k.mu.RUnlock()

	job, failed := k.installJob(id)
	st.InstallError = failed
	if job != nil {
		st.Status = "installing"
		return st
	}
	var replicas int32
	if sts, err := k.sts.StatefulSets(k.ns).Get(stsName(id)); err == nil && sts.Spec.Replicas != nil {
		replicas = *sts.Spec.Replicas
	}
	pod, err := k.pods.Pods(k.ns).Get(podName(id))
	if err != nil {
		if replicas > 0 {
			st.Status = "starting"
		}
		return st
	}
	st.CPU, st.Memory = u.CPU, u.Mem
	if replicas == 0 || pod.DeletionTimestamp != nil {
		st.Status = "stopping"
		return st
	}
	var cs *corev1.ContainerStatus
	for i := range pod.Status.ContainerStatuses {
		if pod.Status.ContainerStatuses[i].Name == mainCtr {
			cs = &pod.Status.ContainerStatuses[i]
		}
	}
	if cs == nil {
		st.Status = "starting"
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse {
				st.Reason = c.Message
			}
		}
		return st
	}
	st.Restarts = cs.RestartCount
	switch {
	case cs.State.Running != nil:
		st.StartedAt = cs.State.Running.StartedAt.Unix()
		if cs.Ready {
			st.Status = "running"
		} else {
			st.Status = "starting"
		}
	case cs.State.Waiting != nil:
		st.Status = "starting"
		if crashReasons[cs.State.Waiting.Reason] {
			st.Status = "crashed"
			st.Reason = strings.TrimSpace(cs.State.Waiting.Reason + " " + cs.State.Waiting.Message)
			if cs.LastTerminationState.Terminated != nil {
				st.Reason = fmt.Sprintf("%s (exit code %d)", st.Reason, cs.LastTerminationState.Terminated.ExitCode)
			}
		}
	case cs.State.Terminated != nil:
		st.Status = "crashed"
		st.Reason = fmt.Sprintf("Exited: %s (code %d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
	}
	return st
}

// LogTarget returns which pod/container the console should follow.
func (k *Kube) LogTarget(id string) (pod, container string, uid types.UID, ok bool) {
	if job, _ := k.installJob(id); job != nil {
		pods, _ := k.pods.Pods(k.ns).List(labels.SelectorFromSet(map[string]string{"job-name": job.Name}))
		for _, p := range pods {
			if len(p.Status.ContainerStatuses) > 0 && p.Status.ContainerStatuses[0].State.Waiting == nil {
				return p.Name, installCtr, p.UID, true
			}
		}
		return "", "", "", false
	}
	p, err := k.pods.Pods(k.ns).Get(podName(id))
	if err != nil {
		return "", "", "", false
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Name == mainCtr && (cs.State.Running != nil || cs.State.Terminated != nil) {
			return p.Name, mainCtr, p.UID, true
		}
	}
	return "", "", "", false
}

// ---------- Metrics

func (k *Kube) metricsLoop(ctx context.Context) {
	t := time.NewTicker(10 * time.Second)
	defer t.Stop()
	for {
		k.fetchMetrics(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type metricsList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Usage      map[string]string `json:"usage"`
		Containers []struct {
			Name  string            `json:"name"`
			Usage map[string]string `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

func parseUsage(u map[string]string) Usage {
	var out Usage
	if q, err := resource.ParseQuantity(u["cpu"]); err == nil {
		out.CPU = float64(q.MilliValue())
		if q.MilliValue() == 0 {
			out.CPU = float64(q.ScaledValue(resource.Nano)) / 1e6
		}
	}
	if q, err := resource.ParseQuantity(u["memory"]); err == nil {
		out.Mem = q.Value()
	}
	return out
}

func (k *Kube) fetchMetrics(ctx context.Context) {
	raw, err := k.cs.CoreV1().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/namespaces", k.ns, "pods").DoRaw(ctx)
	if err != nil {
		return
	}
	var ml metricsList
	if json.Unmarshal(raw, &ml) != nil {
		return
	}
	m := map[string]Usage{}
	for _, it := range ml.Items {
		for _, c := range it.Containers {
			if c.Name == mainCtr {
				m[it.Metadata.Name] = parseUsage(c.Usage)
			}
		}
	}
	k.mu.Lock()
	k.metrics = m
	k.mu.Unlock()
}

type NodeInfo struct {
	Name    string  `json:"name"`
	Ready   bool    `json:"ready"`
	Version string  `json:"version"`
	OS      string  `json:"os"`
	CPUCap  float64 `json:"cpuCap"`
	MemCap  int64   `json:"memCap"`
	CPU     float64 `json:"cpu"`
	Mem     int64   `json:"mem"`
	Pods    int     `json:"pods"`
	Address string  `json:"address"`
}

func (k *Kube) Nodes(ctx context.Context) ([]NodeInfo, error) {
	nodes, err := k.cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	usage := map[string]Usage{}
	if raw, err := k.cs.CoreV1().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").DoRaw(ctx); err == nil {
		var ml metricsList
		if json.Unmarshal(raw, &ml) == nil {
			for _, it := range ml.Items {
				usage[it.Metadata.Name] = parseUsage(it.Usage)
			}
		}
	}
	counts := map[string]int{}
	if pods, err := k.pods.Pods(k.ns).List(labels.SelectorFromSet(map[string]string{lblRole: "server"})); err == nil {
		for _, p := range pods {
			if p.Status.Phase == corev1.PodRunning || p.Status.Phase == corev1.PodPending {
				counts[p.Spec.NodeName]++
			}
		}
	}
	out := []NodeInfo{}
	for _, n := range nodes.Items {
		ni := NodeInfo{Name: n.Name, Version: n.Status.NodeInfo.KubeletVersion, OS: n.Status.NodeInfo.OSImage, Pods: counts[n.Name]}
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady {
				ni.Ready = c.Status == corev1.ConditionTrue
			}
		}
		for _, a := range n.Status.Addresses {
			if a.Type == corev1.NodeInternalIP && ni.Address == "" {
				ni.Address = a.Address
			}
		}
		if q, ok := n.Status.Allocatable[corev1.ResourceCPU]; ok {
			ni.CPUCap = float64(q.MilliValue())
		}
		if q, ok := n.Status.Allocatable[corev1.ResourceMemory]; ok {
			ni.MemCap = q.Value()
		}
		u := usage[n.Name]
		ni.CPU, ni.Mem = u.CPU, u.Mem
		out = append(out, ni)
	}
	return out, nil
}

// ---------- Console input (attach to the container's stdin)

type attachSess struct {
	w      *io.PipeWriter
	podUID types.UID
	done   chan struct{}
	cancel context.CancelFunc
}

func (k *Kube) SendStdin(id, line string) error {
	pod, err := k.pods.Pods(k.ns).Get(podName(id))
	if err != nil {
		return errors.New("The server is not running")
	}
	running := false
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == mainCtr && cs.State.Running != nil {
			running = true
		}
	}
	if !running {
		return errors.New("The server is not running")
	}
	write := func() error {
		k.attachMu.Lock()
		s := k.attach[id]
		if s != nil {
			select {
			case <-s.done:
				s = nil
			default:
				if s.podUID != pod.UID {
					s.cancel()
					s = nil
				}
			}
		}
		if s == nil {
			s, err = k.openAttach(pod)
			if err != nil {
				k.attachMu.Unlock()
				return err
			}
			k.attach[id] = s
		}
		k.attachMu.Unlock()
		res := make(chan error, 1)
		go func() { _, err := s.w.Write([]byte(line + "\n")); res <- err }()
		select {
		case err := <-res:
			return err
		case <-s.done:
			return errors.New("Console connection closed")
		case <-time.After(10 * time.Second):
			s.cancel()
			return errors.New("Timed out while sending the command")
		}
	}
	if err := write(); err != nil {
		return write() // one retry with a fresh session
	}
	return nil
}

func (k *Kube) openAttach(pod *corev1.Pod) (*attachSess, error) {
	req := k.cs.CoreV1().RESTClient().Post().Resource("pods").Namespace(k.ns).Name(pod.Name).SubResource("attach").
		VersionedParams(&corev1.PodAttachOptions{Container: mainCtr, Stdin: true}, scheme.ParameterCodec)
	spdy, err := remotecommand.NewSPDYExecutor(k.rc, "POST", req.URL())
	if err != nil {
		return nil, err
	}
	ws, err := remotecommand.NewWebSocketExecutor(k.rc, "GET", req.URL().String())
	if err != nil {
		return nil, err
	}
	exec, err := remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	s := &attachSess{w: pw, podUID: pod.UID, done: make(chan struct{}), cancel: cancel}
	go func() {
		err := exec.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: pr})
		if err != nil && ctx.Err() == nil {
			log.Printf("attach %s: %v", pod.Name, err)
		}
		cancel()
		_ = pr.CloseWithError(io.ErrClosedPipe)
		close(s.done)
	}()
	return s, nil
}

// stopScript sends an egg's stop command: "^C" is SIGINT, anything else is typed in the console (stdin of PID 1,
// which the game shares), then it waits for the game to exit.
func stopScript(cmd string) string {
	send := "printf '%s\\n' " + shellQuote(cmd) + " > /proc/1/fd/0"
	if strings.HasPrefix(cmd, "^") {
		send = "kill -INT -1 2>/dev/null; kill -INT 1"
	}
	return send + "\nwhile kill -0 1 2>/dev/null; do sleep 1; done"
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// eggOverhead: eggs size their process from SERVER_MEMORY (e.g. a JVM with -Xmx{{SERVER_MEMORY}}M or
// MaxRAMPercentage=95), counting on Wings' extra headroom above the server's memory. Same headroom here, or
// the kernel would kill the game as soon as its heap is full.
func eggOverhead(mb int) int {
	switch {
	case mb <= 2048:
		return max(mb*15/100, 256)
	case mb <= 4096:
		return mb * 10 / 100
	}
	return mb * 5 / 100
}
