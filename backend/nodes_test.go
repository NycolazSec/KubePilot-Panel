package main

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testNode(name string, memMB int64, ready bool) corev1.Node {
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: st}},
			Allocatable: corev1.ResourceList{corev1.ResourceMemory: *resource.NewQuantity(memMB<<20, resource.BinarySI)},
		},
	}
}

func TestPickNode(t *testing.T) {
	local := func(n string) bool { return n == "main" }
	agents := map[string]bool{"big": true, "small": true, "noagent": false}
	nodes := []corev1.Node{testNode("main", 8000, true), testNode("big", 16000, true), testNode("small", 4000, true)}
	loads := map[string]*nodeLoad{"main": {servers: 3, mem: 6000}, "big": {servers: 1, mem: 2000}}

	pick := func(want string, mem int, settings map[string]NodeSetting, ns ...corev1.Node) (string, error) {
		if ns == nil {
			ns = nodes
		}
		return pickNode(ns, local, agents, loads, settings, want, mem)
	}
	if n, err := pick("", 1024, nil); err != nil || n != "big" {
		t.Fatalf("most free memory: %q %v", n, err)
	}
	if n, err := pick("", 1024, map[string]NodeSetting{"big": {AcceptNew: false}}); err != nil || n != "small" {
		t.Fatalf("a closed node is skipped: %q %v", n, err)
	}
	if n, err := pick("main", 1024, nil); err != nil || n != "main" {
		t.Fatalf("the admin's choice wins: %q %v", n, err)
	}
	if n, err := pick("big", 1024, map[string]NodeSetting{"big": {AcceptNew: false}}); err != nil || n != "big" {
		t.Fatalf("closed to automatic placement only: %q %v", n, err)
	}
	if _, err := pick("nope", 1024, nil); err == nil {
		t.Fatal("unknown node")
	}

	cordoned := testNode("big", 16000, true)
	cordoned.Spec.Unschedulable = true
	tainted := testNode("small", 4000, true)
	tainted.Spec.Taints = []corev1.Taint{{Key: "dedicated", Effect: corev1.TaintEffectNoSchedule}}
	if n, err := pick("", 1024, nil, testNode("main", 8000, true), cordoned, tainted); err != nil || n != "main" {
		t.Fatalf("cordoned and tainted nodes are skipped: %q %v", n, err)
	}
	if _, err := pick("big", 1024, nil, testNode("main", 8000, true), cordoned); err == nil || !strings.Contains(err.Error(), "cordoned") {
		t.Fatalf("an explicit choice still needs a usable node: %v", err)
	}
	if n, err := pick("", 1024, nil, testNode("main", 8000, true), testNode("noagent", 64000, true)); err != nil || n != "main" {
		t.Fatalf("a node whose agent isn't running is skipped: %q %v", n, err)
	}
	if n, err := pick("", 1024, nil, testNode("main", 8000, true), testNode("big", 64000, false)); err != nil || n != "main" {
		t.Fatalf("a node that isn't ready is skipped: %q %v", n, err)
	}
	if _, err := pick("", 40000, nil); err == nil {
		t.Fatal("no node has room for 40 GB")
	}
	if n, err := pick("", 1024, nil, testNode("main", 8000, true)); err != nil || n != "" {
		t.Fatalf("one node: nothing pinned: %q %v", n, err)
	}
	// equal free memory: the node with fewer servers, then by name
	if n, _ := pickNode([]corev1.Node{testNode("b", 4000, true), testNode("a", 4000, true)}, func(string) bool { return true }, nil,
		map[string]*nodeLoad{"a": {servers: 2}}, nil, "", 512); n != "b" {
		t.Fatalf("tie: %q", n)
	}
}

func TestPVNodeAndAffinity(t *testing.T) {
	pv := &corev1.PersistentVolume{Spec: corev1.PersistentVolumeSpec{NodeAffinity: &corev1.VolumeNodeAffinity{Required: &corev1.NodeSelector{
		NodeSelectorTerms: []corev1.NodeSelectorTerm{{MatchExpressions: []corev1.NodeSelectorRequirement{
			{Key: corev1.LabelHostname, Operator: corev1.NodeSelectorOpIn, Values: []string{"node-b"}},
		}}},
	}}}}
	if n := pvNode(pv); n != "node-b" {
		t.Fatalf("pvNode: %q", n)
	}
	if pvNode(&corev1.PersistentVolume{}) != "" || onNode("") != nil {
		t.Fatal("no node")
	}
	a := onNode("node-b")
	f := a.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchFields[0]
	if f.Key != "metadata.name" || f.Values[0] != "node-b" {
		t.Fatalf("affinity: %+v", f)
	}
	sp := Spec{Server: &Server{ID: "x", Node: "node-b"}, Template: &Template{DataPath: "/data"}}
	if sp.podTemplate().Spec.Affinity == nil {
		t.Fatal("a placed server's pod is pinned to its node")
	}
	sp.Server.Node = ""
	if sp.podTemplate().Spec.Affinity != nil {
		t.Fatal("an unplaced server's pod is not pinned")
	}
}

func TestValidHost(t *testing.T) {
	for h, ok := range map[string]bool{"play.example.com": true, "51.75.1.2": true, "2001:db8::1": true, "": false,
		"http://x": false, "a b": false, "x:25565": false, "UPPER.example.com": false} {
		if validHost(h) != ok {
			t.Errorf("%q: want %v", h, ok)
		}
	}
}
