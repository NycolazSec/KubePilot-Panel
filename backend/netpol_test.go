package main

import (
	"testing"

	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestParseIngressPeer(t *testing.T) {
	ing, err := parseIngressPeer(" kube-system ", "app.kubernetes.io/name=traefik", "web, websecure,8443")
	if err != nil {
		t.Fatal(err)
	}
	if ing.Namespace != "kube-system" || ing.Labels["app.kubernetes.io/name"] != "traefik" || len(ing.Labels) != 1 {
		t.Fatalf("peer: %+v", ing)
	}
	want := []intstr.IntOrString{intstr.FromString("web"), intstr.FromString("websecure"), intstr.FromInt32(8443)}
	if len(ing.Ports) != len(want) {
		t.Fatalf("ports: %v", ing.Ports)
	}
	for i := range want {
		if ing.Ports[i] != want[i] {
			t.Fatalf("port %d: %v, want %v", i, ing.Ports[i], want[i])
		}
	}
	if ing, err := parseIngressPeer("", "a=b", "web"); err != nil || ing.Namespace != "" {
		t.Fatalf("no namespace = off: %+v %v", ing, err)
	}
	for _, c := range [][3]string{
		{"kube-system", "", "web"},                          // empty selector: every pod of the namespace
		{"kube-system", "app=traefik", ""},                  // no port: every port
		{"kube-system", "app=traefik", " , "},               // same
		{"kube-system", "app=traefik", "0"},                 // out of range
		{"kube-system", "app=traefik", "70000"},             // out of range
		{"kube-system", "app=traefik", "not_a-valid-name!"}, // invalid port name
		{"Kube_System", "app=traefik", "web"},               // invalid namespace
		{"kube-system", "app=traefik,=x", "web"},            // invalid label
	} {
		if _, err := parseIngressPeer(c[0], c[1], c[2]); err == nil {
			t.Errorf("%q accepted", c)
		}
	}
}

func TestIsolationPolicy(t *testing.T) {
	np := isolationPolicy("games", []string{"51.68.227.100", "10.0.0.5", "51.68.227.100", "2001:db8::1", "node-1", ""}, IngressPeer{})
	if np.Namespace != "games" || len(np.Spec.Egress) != 3 {
		t.Fatalf("policy: %+v", np.Spec.Egress)
	}
	except := np.Spec.Egress[0].To[0].IPBlock.Except
	if n := len(except); n != 6 || except[5] != "51.68.227.100/32" {
		t.Fatalf("the node's public IP is refused once, private ones are already covered, IPv6 and names are skipped: %v", except)
	}

	ing, _ := parseIngressPeer("kube-system", "app.kubernetes.io/name=traefik", "web,websecure")
	np = isolationPolicy("games", nil, ing)
	if len(np.Spec.Egress) != 4 {
		t.Fatalf("no ingress rule: %+v", np.Spec.Egress)
	}
	r := np.Spec.Egress[3]
	if len(r.To) != 1 || r.To[0].IPBlock != nil ||
		r.To[0].NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "kube-system" ||
		r.To[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "traefik" {
		t.Fatalf("peer: %+v", r.To)
	}
	if len(r.Ports) != 2 || r.Ports[0].Port.StrVal != "web" || r.Ports[1].Port.StrVal != "websecure" || string(*r.Ports[1].Protocol) != "TCP" {
		t.Fatalf("ports: %+v", r.Ports)
	}
	if *r.Ports[0].Port == *r.Ports[1].Port {
		t.Fatal("ports share one pointer")
	}
}
