package endpoints

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

func kubeStatusFor(fwdPort, ingressPort int) model.KubeStatus {
	return model.KubeStatus{
		WorkflowID: "wf",
		Cluster: model.KubeClusterStatus{Name: "nimbo-k8s-wf", State: model.ClusterRunning, Version: "v1.36.5+k3s1", APIPort: 61443,
			IngressPort: ingressPort, Kubeconfig: "/home/u/.nimbo/workflows/wf/kube/kubeconfig", IngressClass: "traefik", StorageClass: "local-path"},
		Forwards: []model.KubeForward{
			{NodeID: "Service/default/web", Namespace: "default", Service: "web", Port: 80, PortName: "http", Protocol: "TCP", HostPort: fwdPort, URL: fmt.Sprintf("http://127.0.0.1:%d/", fwdPort), Ready: true},
			{NodeID: "Service/shop/db", Namespace: "shop", Service: "db", Port: 5432, Protocol: "TCP", HostPort: 55432},
		},
		Ingresses: []model.KubeIngressURL{{NodeID: "Ingress/default/web", Namespace: "default", Ingress: "web", Host: "web.shop.localhost", Path: "/",
			Service: "web", Port: "80", URL: fmt.Sprintf("http://web.shop.localhost:%d/", ingressPort), Address: fmt.Sprintf("http://127.0.0.1:%d/", ingressPort), ServiceNodeID: "Service/default/web"}},
	}
}

func TestBuildKube(t *testing.T) {
	info := BuildKube(&model.Workflow{ID: "wf"}, kubeStatusFor(8080, 61080))
	if len(info.Resources) != 4 {
		t.Fatalf("cards = %+v", info.Resources)
	}
	cluster, ing, web, db := info.Resources[0], info.Resources[1], info.Resources[2], info.Resources[3]
	if cluster.Kind != KindKubernetes || cluster.NodeID != "" || field(cluster, "Kubeconfig") != "/home/u/.nimbo/workflows/wf/kube/kubeconfig" || field(cluster, "API server") != "https://127.0.0.1:61443" {
		t.Errorf("cluster = %+v", cluster)
	}
	if web.Kind != KindWebService || web.NodeID != "Service/default/web" || field(web, "Host URL (port 80)") != "http://127.0.0.1:8080/" ||
		field(web, "In-cluster address (port 80)") != "web.default.svc.cluster.local:80" || len(web.Actions) != 1 || web.Warning != "" {
		t.Errorf("web = %+v", web)
	}
	if db.Kind != KindDatabase || db.Label != "shop/db" || field(db, "Port") != "55432" || db.Warning == "" || !strings.Contains(snippet(t, db, "psql"), "-p 55432") {
		t.Errorf("db = %+v", db)
	}
	if ing.Kind != KindWebService || ing.NodeID != "Ingress/default/web" || field(ing, "URL web.shop.localhost/") != "http://web.shop.localhost:61080/" ||
		!strings.Contains(snippet(t, ing, "curl"), "-H 'Host: web.shop.localhost' http://127.0.0.1:61080/") {
		t.Errorf("ingress = %+v", ing)
	}
	stopped := kubeStatusFor(8080, 61080)
	stopped.Cluster.State = model.ClusterStopped
	if got := BuildKube(nil, stopped); len(got.Resources) != 0 {
		t.Errorf("stopped cluster cards = %+v", got.Resources)
	}
}

func TestTryKube(t *testing.T) {
	var hosts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts = append(hosts, r.Host+" "+r.URL.Path)
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()
	_, p, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	var port int
	fmt.Sscan(p, &port)
	ctx := context.Background()

	// The same server stands in for the forward and the ingress port.
	st := kubeStatusFor(port, port)
	res, err := TryKube(ctx, st, model.TryRequest{NodeID: "Service/default/web", Action: ActRequest, Path: "/health"})
	if err != nil || !res.OK {
		t.Fatalf("service try: %+v %v", res, err)
	}
	res, err = TryKube(ctx, st, model.TryRequest{NodeID: "Ingress/default/web", Action: ActRequest})
	if err != nil || !res.OK {
		t.Fatalf("ingress try: %+v %v", res, err)
	}
	res, err = TryKube(ctx, st, model.TryRequest{NodeID: "Ingress/default/web", Action: ActRequest, Path: fmt.Sprintf("http://other.shop.localhost:%d/x", port)})
	if err != nil || !res.OK {
		t.Fatalf("ingress full URL: %+v %v", res, err)
	}
	want := []string{"127.0.0.1:" + p + " /health", "web.shop.localhost /", "other.shop.localhost /x"}
	if strings.Join(hosts, ",") != strings.Join(want, ",") {
		t.Errorf("requests = %v, want %v", hosts, want)
	}
	if _, err := TryKube(ctx, st, model.TryRequest{NodeID: "Service/default/web", Action: ActRequest, Path: "http://127.0.0.1:1/"}); err == nil {
		t.Error("a port that is not forwarded was called")
	}
	if _, err := TryKube(ctx, st, model.TryRequest{NodeID: "Service/shop/db", Action: ActRequest}); err == nil {
		t.Error("request on a non-HTTP Service")
	}
	if _, err := TryKube(ctx, st, model.TryRequest{NodeID: "Service/x/y", Action: ActRequest}); err != ErrNotFound {
		t.Errorf("unknown node: %v", err)
	}

	col := PostmanKube(&model.Workflow{Name: "Shop"}, st)
	if len(col.Item) != 2 || col.Item[0].Item[0].Request.Header[0].Value != "web.shop.localhost" {
		t.Errorf("postman = %+v", col)
	}
}
