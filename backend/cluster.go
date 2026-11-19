package main

// Cluster view: every workload / pod of the cluster, read-only everywhere, with a few
// safe actions (scale, delete pod, pod-by-pod restart) in the namespaces where the
// panel's ServiceAccount was granted them (RoleBinding "kubepilot-ops").
// The panel never gets the "patch" verb on workloads, so it can't rewrite a pod spec.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	authv1 "k8s.io/api/authorization/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var dnsNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]{0,251}[a-z0-9])?$`)

var systemNamespaces = map[string]bool{
	"kube-system": true, "kube-public": true, "kube-node-lease": true, "cert-manager": true,
	"kubernetes-dashboard": true, "kubepilot-system": true,
}

type opsCache struct {
	mu      sync.Mutex
	allowed map[string]bool
	expires time.Time
}

var nsOps = &opsCache{}

// canOperate asks the API server (SelfSubjectAccessReview) whether the panel may delete pods in ns.
func (a *App) canOperate(ctx context.Context, ns string) bool {
	if ns == a.kube.ns || ns == "kubepilot-system" {
		return false // game servers are managed from their own pages; never touch the panel itself
	}
	nsOps.mu.Lock()
	if time.Now().After(nsOps.expires) {
		nsOps.allowed, nsOps.expires = map[string]bool{}, time.Now().Add(time.Minute)
	}
	v, ok := nsOps.allowed[ns]
	nsOps.mu.Unlock()
	if ok {
		return v
	}
	res, err := a.kube.cs.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{ResourceAttributes: &authv1.ResourceAttributes{Namespace: ns, Verb: "delete", Resource: "pods"}},
	}, metav1.CreateOptions{})
	allowed := err == nil && res.Status.Allowed
	nsOps.mu.Lock()
	nsOps.allowed[ns] = allowed
	nsOps.mu.Unlock()
	return allowed
}

type CWorkload struct {
	Kind      string   `json:"kind"`
	Namespace string   `json:"namespace"`
	Name      string   `json:"name"`
	Desired   int32    `json:"desired"`
	Ready     int32    `json:"ready"`
	Images    []string `json:"images"`
	CreatedAt int64    `json:"createdAt"`
	ServerID  string   `json:"serverId,omitempty"`
	Saved     int32    `json:"savedReplicas,omitempty"`
}

type CPod struct {
	Namespace  string   `json:"namespace"`
	Name       string   `json:"name"`
	Status     string   `json:"status"`
	Ready      int      `json:"ready"`
	Total      int      `json:"total"`
	Restarts   int32    `json:"restarts"`
	Node       string   `json:"node"`
	IP         string   `json:"ip"`
	CreatedAt  int64    `json:"createdAt"`
	Containers []string `json:"containers"`
	Owner      string   `json:"owner"`
	CPU        *float64 `json:"cpu,omitempty"`
	Mem        *int64   `json:"mem,omitempty"`
	ServerID   string   `json:"serverId,omitempty"`
	Stuck      bool     `json:"stuck"`
}

type CNamespace struct {
	Name      string `json:"name"`
	System    bool   `json:"system"`
	CanManage bool   `json:"canManage"`
}

func podStatus(p *corev1.Pod) string {
	if p.DeletionTimestamp != nil {
		return "Terminating"
	}
	for _, c := range p.Status.InitContainerStatuses {
		if c.State.Waiting != nil && c.State.Waiting.Reason != "PodInitializing" {
			return "Init:" + c.State.Waiting.Reason
		}
	}
	for _, c := range p.Status.ContainerStatuses {
		if c.State.Waiting != nil && c.State.Waiting.Reason != "" {
			return c.State.Waiting.Reason
		}
		if c.State.Terminated != nil && c.State.Terminated.Reason != "" && p.Status.Phase != corev1.PodSucceeded {
			return c.State.Terminated.Reason
		}
	}
	if p.Status.Reason != "" {
		return p.Status.Reason
	}
	return string(p.Status.Phase)
}

func (a *App) handleClusterOverview(w http.ResponseWriter, r *http.Request, u *User) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cs := a.kube.cs
	all := metav1.ListOptions{}

	nss, err := cs.CoreV1().Namespaces().List(ctx, all)
	if err != nil {
		writeErr(w, fmt.Errorf("liste des namespaces : %w", err))
		return
	}
	pods, err := cs.CoreV1().Pods("").List(ctx, all)
	if err != nil {
		writeErr(w, fmt.Errorf("liste des pods : %w", err))
		return
	}
	deps, _ := cs.AppsV1().Deployments("").List(ctx, all)
	stss, _ := cs.AppsV1().StatefulSets("").List(ctx, all)
	dss, _ := cs.AppsV1().DaemonSets("").List(ctx, all)
	rss, _ := cs.AppsV1().ReplicaSets("").List(ctx, all)
	ings, _ := cs.NetworkingV1().Ingresses("").List(ctx, all)
	svcs, _ := cs.CoreV1().Services("").List(ctx, all)
	evs, _ := cs.CoreV1().Events("").List(ctx, metav1.ListOptions{FieldSelector: "type=Warning", Limit: 300})

	// pod metrics (all namespaces)
	usage := map[string]Usage{}
	if raw, err := cs.CoreV1().RESTClient().Get().AbsPath("/apis/metrics.k8s.io/v1beta1/pods").DoRaw(ctx); err == nil {
		var ml metricsList
		if json.Unmarshal(raw, &ml) == nil {
			for _, it := range ml.Items {
				var sum Usage
				for _, c := range it.Containers {
					x := parseUsage(c.Usage)
					sum.CPU += x.CPU
					sum.Mem += x.Mem
				}
				usage[it.Metadata.Namespace+"/"+it.Metadata.Name] = sum
			}
		}
	}
	// ReplicaSet → Deployment
	rsOwner := map[string]string{}
	if rss != nil {
		for _, rs := range rss.Items {
			for _, o := range rs.OwnerReferences {
				if o.Kind == "Deployment" {
					rsOwner[rs.Namespace+"/"+rs.Name] = o.Name
				}
			}
		}
	}
	// nodes that are down → their pods are "stuck"
	deadNodes := map[string]bool{}
	if nodes, err := cs.CoreV1().Nodes().List(ctx, all); err == nil {
		for _, n := range nodes.Items {
			for _, c := range n.Status.Conditions {
				if c.Type == corev1.NodeReady && c.Status != corev1.ConditionTrue {
					deadNodes[n.Name] = true
				}
			}
		}
	}

	outNs := []CNamespace{}
	for _, n := range nss.Items {
		outNs = append(outNs, CNamespace{Name: n.Name, System: systemNamespaces[n.Name], CanManage: a.canOperate(ctx, n.Name)})
	}

	saved := map[string]int32{}
	if rows, err := a.db.Query(`SELECT key, replicas FROM cluster_scale`); err == nil {
		for rows.Next() {
			var k string
			var v int32
			if rows.Scan(&k, &v) == nil {
				saved[k] = v
			}
		}
		rows.Close()
	}

	workloads := []CWorkload{}
	images := func(spec corev1.PodSpec) []string {
		out := []string{}
		for _, c := range spec.Containers {
			out = append(out, c.Image)
		}
		return out
	}
	if deps != nil {
		for _, d := range deps.Items {
			var want int32 = 1
			if d.Spec.Replicas != nil {
				want = *d.Spec.Replicas
			}
			workloads = append(workloads, CWorkload{Kind: "Deployment", Namespace: d.Namespace, Name: d.Name, Desired: want, Ready: d.Status.ReadyReplicas,
				Images: images(d.Spec.Template.Spec), CreatedAt: d.CreationTimestamp.Unix(), Saved: saved["Deployment/"+d.Namespace+"/"+d.Name]})
		}
	}
	if stss != nil {
		for _, s := range stss.Items {
			var want int32 = 1
			if s.Spec.Replicas != nil {
				want = *s.Spec.Replicas
			}
			workloads = append(workloads, CWorkload{Kind: "StatefulSet", Namespace: s.Namespace, Name: s.Name, Desired: want, Ready: s.Status.ReadyReplicas,
				Images: images(s.Spec.Template.Spec), CreatedAt: s.CreationTimestamp.Unix(), ServerID: s.Labels[lblServer], Saved: saved["StatefulSet/"+s.Namespace+"/"+s.Name]})
		}
	}
	if dss != nil {
		for _, d := range dss.Items {
			workloads = append(workloads, CWorkload{Kind: "DaemonSet", Namespace: d.Namespace, Name: d.Name, Desired: d.Status.DesiredNumberScheduled, Ready: d.Status.NumberReady,
				Images: images(d.Spec.Template.Spec), CreatedAt: d.CreationTimestamp.Unix()})
		}
	}
	sort.Slice(workloads, func(i, j int) bool {
		if workloads[i].Namespace != workloads[j].Namespace {
			return workloads[i].Namespace < workloads[j].Namespace
		}
		return workloads[i].Name < workloads[j].Name
	})

	outPods := []CPod{}
	for i := range pods.Items {
		p := &pods.Items[i]
		cp := CPod{Namespace: p.Namespace, Name: p.Name, Status: podStatus(p), Node: p.Spec.NodeName, IP: p.Status.PodIP,
			CreatedAt: p.CreationTimestamp.Unix(), ServerID: p.Labels[lblServer]}
		for _, c := range p.Spec.Containers {
			cp.Containers = append(cp.Containers, c.Name)
		}
		for _, c := range p.Status.ContainerStatuses {
			cp.Total++
			cp.Restarts += c.RestartCount
			if c.Ready {
				cp.Ready++
			}
		}
		if cp.Total == 0 {
			cp.Total = len(p.Spec.Containers)
		}
		if len(p.OwnerReferences) > 0 {
			o := p.OwnerReferences[0]
			cp.Owner = o.Kind + "/" + o.Name
			if o.Kind == "ReplicaSet" {
				if d, ok := rsOwner[p.Namespace+"/"+o.Name]; ok {
					cp.Owner = "Deployment/" + d
				}
			}
		}
		if us, ok := usage[p.Namespace+"/"+p.Name]; ok {
			c, m := us.CPU, us.Mem
			cp.CPU, cp.Mem = &c, &m
		}
		cp.Stuck = p.DeletionTimestamp != nil && (deadNodes[p.Spec.NodeName] || time.Since(p.DeletionTimestamp.Time) > 10*time.Minute)
		outPods = append(outPods, cp)
	}
	sort.Slice(outPods, func(i, j int) bool {
		if outPods[i].Namespace != outPods[j].Namespace {
			return outPods[i].Namespace < outPods[j].Namespace
		}
		return outPods[i].Name < outPods[j].Name
	})

	type ingRow struct {
		Namespace string   `json:"namespace"`
		Name      string   `json:"name"`
		Host      string   `json:"host"`
		TLS       bool     `json:"tls"`
		Backends  []string `json:"backends"`
	}
	outIng := []ingRow{}
	if ings != nil {
		for _, ing := range ings.Items {
			tls := map[string]bool{}
			for _, t := range ing.Spec.TLS {
				for _, h := range t.Hosts {
					tls[h] = true
				}
			}
			for _, rule := range ing.Spec.Rules {
				row := ingRow{Namespace: ing.Namespace, Name: ing.Name, Host: rule.Host, TLS: tls[rule.Host]}
				if row.Host == "" {
					row.Host = "*"
				}
				if rule.HTTP != nil {
					for _, p := range rule.HTTP.Paths {
						if p.Backend.Service != nil {
							port := p.Backend.Service.Port.Name
							if p.Backend.Service.Port.Number != 0 {
								port = fmt.Sprint(p.Backend.Service.Port.Number)
							}
							row.Backends = append(row.Backends, fmt.Sprintf("%s → %s:%s", p.Path, p.Backend.Service.Name, port))
						}
					}
				}
				outIng = append(outIng, row)
			}
		}
	}
	type svcRow struct {
		Namespace string   `json:"namespace"`
		Name      string   `json:"name"`
		Type      string   `json:"type"`
		ClusterIP string   `json:"clusterIP"`
		Ports     []string `json:"ports"`
	}
	outSvc := []svcRow{}
	if svcs != nil {
		for _, s := range svcs.Items {
			row := svcRow{Namespace: s.Namespace, Name: s.Name, Type: string(s.Spec.Type), ClusterIP: s.Spec.ClusterIP}
			for _, p := range s.Spec.Ports {
				txt := fmt.Sprintf("%d/%s", p.Port, p.Protocol)
				if p.NodePort != 0 {
					txt = fmt.Sprintf("%d:%d/%s", p.Port, p.NodePort, p.Protocol)
				}
				row.Ports = append(row.Ports, txt)
			}
			outSvc = append(outSvc, row)
		}
	}
	type evRow struct {
		Namespace string `json:"namespace"`
		Object    string `json:"object"`
		Reason    string `json:"reason"`
		Message   string `json:"message"`
		Count     int32  `json:"count"`
		Last      int64  `json:"last"`
	}
	outEv := []evRow{}
	if evs != nil {
		for _, e := range evs.Items {
			last := e.LastTimestamp.Unix()
			if e.LastTimestamp.IsZero() {
				last = e.EventTime.Unix()
				if e.EventTime.IsZero() {
					last = e.CreationTimestamp.Unix()
				}
			}
			cnt := e.Count
			if cnt == 0 {
				cnt = 1
			}
			outEv = append(outEv, evRow{Namespace: e.Namespace, Object: e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name, Reason: e.Reason, Message: e.Message, Count: cnt, Last: last})
		}
		sort.Slice(outEv, func(i, j int) bool { return outEv[i].Last > outEv[j].Last })
		if len(outEv) > 100 {
			outEv = outEv[:100]
		}
	}

	writeJSON(w, 200, map[string]any{
		"namespaces": outNs, "workloads": workloads, "pods": outPods, "ingresses": outIng, "services": outSvc,
		"events": outEv, "metrics": len(usage) > 0, "gameNamespace": a.kube.ns,
	})
}

// ---------- Actions

type clusterAction struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Replicas  *int32 `json:"replicas"`
	Force     bool   `json:"force"`
}

func (a *App) clusterGuard(w http.ResponseWriter, r *http.Request, in *clusterAction) bool {
	if err := readJSON(r, in); err != nil {
		writeErr(w, err)
		return false
	}
	if !dnsNameRe.MatchString(in.Namespace) || !dnsNameRe.MatchString(in.Name) {
		writeErr(w, errCode(400, "Nom invalide"))
		return false
	}
	if !a.canOperate(r.Context(), in.Namespace) {
		writeErr(w, errCode(403, "Namespace en lecture seule : ajoute-le à KUBEPILOT_OPS_NAMESPACES pour y autoriser des actions"))
		return false
	}
	return true
}

func (a *App) getScale(ctx context.Context, kind, ns, name string) (*autoscalingv1.Scale, error) {
	switch kind {
	case "Deployment":
		return a.kube.cs.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
	case "StatefulSet":
		return a.kube.cs.AppsV1().StatefulSets(ns).GetScale(ctx, name, metav1.GetOptions{})
	}
	return nil, errCode(400, "Seuls les Deployments et StatefulSets se mettent à l'échelle")
}

func (a *App) setScale(ctx context.Context, kind, ns, name string, n int32) error {
	sc, err := a.getScale(ctx, kind, ns, name)
	if err != nil {
		return err
	}
	if n == 0 && sc.Spec.Replicas > 0 {
		_, _ = a.db.Exec(`INSERT INTO cluster_scale(key, replicas) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET replicas=excluded.replicas`,
			kind+"/"+ns+"/"+name, sc.Spec.Replicas)
	}
	sc.Spec.Replicas = n
	switch kind {
	case "Deployment":
		_, err = a.kube.cs.AppsV1().Deployments(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{})
	case "StatefulSet":
		_, err = a.kube.cs.AppsV1().StatefulSets(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{})
	}
	return err
}

func (a *App) handleClusterScale(w http.ResponseWriter, r *http.Request, u *User) {
	var in clusterAction
	if !a.clusterGuard(w, r, &in) {
		return
	}
	var n int32
	if in.Replicas == nil { // "start": restore the saved count
		n = 1
		var saved int32
		if a.db.QueryRow(`SELECT replicas FROM cluster_scale WHERE key=?`, in.Kind+"/"+in.Namespace+"/"+in.Name).Scan(&saved) == nil && saved > 0 {
			n = saved
		}
	} else {
		n = *in.Replicas
	}
	if n < 0 || n > 20 {
		writeErr(w, errCode(400, "Répliques entre 0 et 20"))
		return
	}
	if err := a.setScale(r.Context(), in.Kind, in.Namespace, in.Name, n); err != nil {
		writeErr(w, err)
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.cluster.scale", fmt.Sprintf("%s %s/%s → %d", in.Kind, in.Namespace, in.Name, n))
	writeJSON(w, 200, map[string]any{"ok": true, "replicas": n})
}

func (a *App) handleClusterDeletePod(w http.ResponseWriter, r *http.Request, u *User) {
	var in clusterAction
	if !a.clusterGuard(w, r, &in) {
		return
	}
	opts := metav1.DeleteOptions{}
	if in.Force {
		zero := int64(0)
		opts.GracePeriodSeconds = &zero
	}
	if err := a.kube.cs.CoreV1().Pods(in.Namespace).Delete(r.Context(), in.Name, opts); err != nil && !apierrors.IsNotFound(err) {
		writeErr(w, err)
		return
	}
	action := "admin.cluster.pod.delete"
	if in.Force {
		action = "admin.cluster.pod.force-delete"
	}
	a.db.LogActivity("", u, clientIP(r), action, in.Namespace+"/"+in.Name)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var restarting sync.Map

// handleClusterRestart replaces the workload's pods one by one, waiting for readiness in between.
func (a *App) handleClusterRestart(w http.ResponseWriter, r *http.Request, u *User) {
	var in clusterAction
	if !a.clusterGuard(w, r, &in) {
		return
	}
	cs := a.kube.cs
	var sel *metav1.LabelSelector
	switch in.Kind {
	case "Deployment":
		d, err := cs.AppsV1().Deployments(in.Namespace).Get(r.Context(), in.Name, metav1.GetOptions{})
		if err != nil {
			writeErr(w, err)
			return
		}
		sel = d.Spec.Selector
	case "StatefulSet":
		s, err := cs.AppsV1().StatefulSets(in.Namespace).Get(r.Context(), in.Name, metav1.GetOptions{})
		if err != nil {
			writeErr(w, err)
			return
		}
		sel = s.Spec.Selector
	case "DaemonSet":
		d, err := cs.AppsV1().DaemonSets(in.Namespace).Get(r.Context(), in.Name, metav1.GetOptions{})
		if err != nil {
			writeErr(w, err)
			return
		}
		sel = d.Spec.Selector
	default:
		writeErr(w, errCode(400, "Type inconnu"))
		return
	}
	selector, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		writeErr(w, err)
		return
	}
	key := in.Kind + "/" + in.Namespace + "/" + in.Name
	if _, busy := restarting.LoadOrStore(key, true); busy {
		writeErr(w, errCode(409, "Redémarrage déjà en cours"))
		return
	}
	a.db.LogActivity("", u, clientIP(r), "admin.cluster.restart", key)
	go func() {
		defer restarting.Delete(key)
		ctx, cancel := context.WithTimeout(a.ctx, 15*time.Minute)
		defer cancel()
		pods, err := cs.CoreV1().Pods(in.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
		if err != nil {
			return
		}
		for _, p := range pods.Items {
			if p.DeletionTimestamp != nil {
				continue
			}
			if err := cs.CoreV1().Pods(in.Namespace).Delete(ctx, p.Name, metav1.DeleteOptions{}); err != nil {
				continue
			}
			// wait until the old pod is gone and the workload is fully ready again
			deadline := time.Now().Add(3 * time.Minute)
			for time.Now().Before(deadline) {
				time.Sleep(3 * time.Second)
				// replaced = old pod gone (or, for a StatefulSet, recreated with a new UID)
				if cur, err := cs.CoreV1().Pods(in.Namespace).Get(ctx, p.Name, metav1.GetOptions{}); err == nil && cur.UID == p.UID {
					continue
				}
				if a.workloadReady(ctx, in.Kind, in.Namespace, in.Name) {
					break
				}
			}
		}
	}()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) workloadReady(ctx context.Context, kind, ns, name string) bool {
	cs := a.kube.cs
	switch kind {
	case "Deployment":
		d, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
		return err == nil && d.Spec.Replicas != nil && d.Status.ReadyReplicas >= *d.Spec.Replicas && d.Status.UnavailableReplicas == 0
	case "StatefulSet":
		s, err := cs.AppsV1().StatefulSets(ns).Get(ctx, name, metav1.GetOptions{})
		return err == nil && s.Spec.Replicas != nil && s.Status.ReadyReplicas >= *s.Spec.Replicas
	case "DaemonSet":
		d, err := cs.AppsV1().DaemonSets(ns).Get(ctx, name, metav1.GetOptions{})
		return err == nil && d.Status.NumberReady >= d.Status.DesiredNumberScheduled
	}
	return true
}

// ---------- Live logs of any pod (WebSocket)

func (a *App) handleClusterLogs(w http.ResponseWriter, r *http.Request, u *User) {
	q := r.URL.Query()
	ns, pod, ctr := q.Get("namespace"), q.Get("pod"), q.Get("container")
	previous := q.Get("previous") == "1"
	if !dnsNameRe.MatchString(ns) || !dnsNameRe.MatchString(pod) || (ctr != "" && !dnsNameRe.MatchString(ctr)) {
		writeErr(w, errCode(400, "Nom invalide"))
		return
	}
	tail := int64(500)
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() { // detect client close
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				cancel()
				return
			}
		}
	}()
	send := func(m WSMsg) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
		return conn.WriteJSON(m) == nil
	}
	opts := &corev1.PodLogOptions{Container: ctr, Follow: !previous, Previous: previous, TailLines: &tail, Timestamps: false}
	stream, err := a.kube.cs.CoreV1().Pods(ns).GetLogs(pod, opts).Stream(ctx)
	if err != nil {
		send(WSMsg{Type: "error", Data: err.Error()})
		return
	}
	defer stream.Close()
	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	batch := []string{}
	flush := time.NewTicker(150 * time.Millisecond)
	defer flush.Stop()
	lines := make(chan string, 1024)
	go func() {
		defer close(lines)
		for sc.Scan() {
			select {
			case lines <- strings.TrimRight(sc.Text(), "\r"):
			case <-ctx.Done():
				return
			}
		}
	}()
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				if len(batch) > 0 {
					send(WSMsg{Type: "lines", Lines: batch})
				}
				send(WSMsg{Type: "end"})
				return
			}
			batch = append(batch, l)
			if len(batch) >= 500 {
				if !send(WSMsg{Type: "lines", Lines: batch}) {
					return
				}
				batch = batch[:0]
			}
		case <-flush.C:
			if len(batch) > 0 {
				if !send(WSMsg{Type: "lines", Lines: batch}) {
					return
				}
				batch = batch[:0]
			}
		case <-ctx.Done():
			return
		}
	}
}

var _ = appsv1.SchemeGroupVersion
