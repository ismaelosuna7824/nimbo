package endpoints

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes workflows: one ResourceAccess per Service the daemon forwards
// (node ID = the Service's graph node), one per Ingress (its rule URLs,
// served by the cluster's ingress controller) and one for the cluster
// itself (API server, Nimbo's kubeconfig). Built from the live
// sandbox.KubeStatus.

// kubeEntry is a card plus the HTTP targets Try may call: url, sent to
// address with the Host header host ("" = none).
type kubeEntry struct {
	model.ResourceAccess
	targets []kubeTarget
}

type kubeTarget struct {
	url, address, host string
}

// kubeDBPorts recognizes databases and caches by Service port.
var kubeDBPorts = map[int]string{5432: "postgres", 3306: "mysql", 27017: "mongo", 1433: "sqlserver", 6379: "redis", 11211: "memcached"}

// BuildKube describes how to reach a Kubernetes workflow's Services and
// Ingresses from the host.
func BuildKube(wf *model.Workflow, st model.KubeStatus) model.EndpointsInfo {
	info := model.EndpointsInfo{WorkflowID: st.WorkflowID, Sandbox: []model.SandboxAccess{}, Resources: []model.ResourceAccess{}, UpdatedAt: time.Now().UTC()}
	if wf != nil && info.WorkflowID == "" {
		info.WorkflowID = wf.ID
	}
	for _, e := range kubeEntries(st) {
		info.Resources = append(info.Resources, e.ResourceAccess)
	}
	return info
}

// kubeEntries lists the cluster, then the Ingresses (what users reach, so the
// first HTTP address shown after a deploy is the app's entry), then the
// Services the daemon forwards.
func kubeEntries(st model.KubeStatus) []kubeEntry {
	if st.Cluster.State != model.ClusterRunning {
		return nil
	}
	var out []kubeEntry
	out = append(out, clusterCard(st))
	kc := "kubectl --kubeconfig " + shq(st.Cluster.Kubeconfig)
	var order []string
	byIng := map[string][]model.KubeIngressURL{}
	for _, u := range st.Ingresses {
		if u.URL == "" {
			continue
		}
		if _, ok := byIng[u.NodeID]; !ok {
			order = append(order, u.NodeID)
		}
		byIng[u.NodeID] = append(byIng[u.NodeID], u)
	}
	for _, id := range order {
		out = append(out, ingressCard(byIng[id], st, kc))
	}
	order = order[:0]
	bySvc := map[string][]model.KubeForward{}
	for _, f := range st.Forwards {
		if _, ok := bySvc[f.NodeID]; !ok {
			order = append(order, f.NodeID)
		}
		bySvc[f.NodeID] = append(bySvc[f.NodeID], f)
	}
	for _, id := range order {
		out = append(out, serviceCard(bySvc[id], kc))
	}
	return out
}

func newKubeEntry(nodeID, typ, label string) kubeEntry {
	return kubeEntry{ResourceAccess: model.ResourceAccess{
		NodeID: nodeID, Address: nodeID, Type: typ, Label: label,
		Fields: []model.AccessField{}, Snippets: []model.Snippet{}, Actions: []model.TryAction{},
	}}
}

func (e *kubeEntry) field(label, value, hint string) {
	if value != "" {
		e.Fields = append(e.Fields, model.AccessField{Label: label, Value: value, Hint: hint})
	}
}

func (e *kubeEntry) snippet(label, lang, code string) {
	e.Snippets = append(e.Snippets, model.Snippet{Label: label, Language: lang, Code: code})
}

func clusterCard(st model.KubeStatus) kubeEntry {
	c := st.Cluster
	e := newKubeEntry("", "Cluster", c.Name)
	e.Address = "cluster:" + c.Name
	e.Kind = KindKubernetes
	e.Summary = "The workflow's local Kubernetes cluster (k3s in one Docker container). Use kubectl or any Kubernetes client with Nimbo's kubeconfig; your own kubeconfig is never changed."
	e.field("Kubeconfig", c.Kubeconfig, "Nimbo's own kubeconfig for this cluster (context "+c.Name+").")
	e.field("Context", c.Name, "")
	if c.APIPort > 0 {
		e.field("API server", fmt.Sprintf("https://127.0.0.1:%d", c.APIPort), "")
	}
	e.field("Kubernetes version", c.Version, "")
	if c.IngressPort > 0 {
		e.field("Ingress", fmt.Sprintf("http://127.0.0.1:%d", c.IngressPort), "The ingress controller ("+c.IngressClass+"); Ingress rules answer here.")
	}
	e.field("Default IngressClass", c.IngressClass, "An IngressClass nginx is served by it too.")
	e.field("Default StorageClass", c.StorageClass, "")
	kc := "kubectl --kubeconfig " + shq(c.Kubeconfig)
	e.snippet("kubectl", "bash", kc+" get pods -A\n"+kc+" get svc,ingress -A")
	e.snippet("Shell environment", "bash", "export KUBECONFIG="+shq(c.Kubeconfig)+"\nkubectl get nodes")
	return e
}

func serviceCard(fs []model.KubeForward, kc string) kubeEntry {
	f0 := fs[0]
	label := f0.Service
	if f0.Namespace != "default" {
		label = f0.Namespace + "/" + f0.Service
	}
	e := newKubeEntry(f0.NodeID, "Service", label)
	dns := f0.Service + "." + f0.Namespace + ".svc.cluster.local"
	ready := true
	db := ""
	var curls []string
	for _, f := range fs {
		ready = ready && f.Ready
		if k := kubeDBPorts[f.Port]; k != "" && db == "" && f.URL == "" {
			db = k
		}
	}
	for _, f := range fs {
		local := fmt.Sprintf("127.0.0.1:%d", f.HostPort)
		if f.HostPort == 0 {
			e.field(fmt.Sprintf("Port %d", f.Port), "not forwarded", f.Message)
			continue
		}
		if f.URL != "" {
			e.targets = append(e.targets, kubeTarget{url: f.URL, address: f.URL})
			e.field(fmt.Sprintf("Host URL (port %d)", f.Port), f.URL, "Forwarded by Nimbo to a ready pod of the Service; the port stays the same across runs.")
			e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: http.MethodGet, Path: "/", URL: f.URL, Description: fmt.Sprintf("%s port %d", f.Service, f.Port)})
			curls = append(curls, "curl -i "+shq(f.URL))
		} else {
			e.field(fmt.Sprintf("Local port (%d/TCP)", f.Port), local, "Forwarded by Nimbo to a ready pod of the Service.")
		}
		e.field(fmt.Sprintf("In-cluster address (port %d)", f.Port), dns+":"+strconv.Itoa(f.Port), "Other pods reach the Service here (or just "+f0.Service+":"+strconv.Itoa(f.Port)+" in the same namespace).")
	}
	if !ready {
		e.Warning = "No ready pod behind this Service yet: run Up, or check the workload's pods; the local port answers once a pod is ready."
	}
	switch {
	case len(e.targets) > 0:
		e.Kind = KindWebService
		e.Summary = "A Service of your cluster serving HTTP, forwarded to your computer."
		e.Actions = append(e.Actions, model.TryAction{ID: ActRequest, Label: "Send a request", Description: "Calls the Service through its local port; change the path if you like.", Input: "http"})
		e.snippet("curl", "bash", strings.Join(curls, "\n\n"))
		e.snippet("JavaScript", "javascript", fmt.Sprintf("const res = await fetch(%q);\nconsole.log(res.status, await res.text());", e.targets[0].url))
		e.snippet("Python", "python", fmt.Sprintf("import urllib.request\n\nwith urllib.request.urlopen(%q) as res:\n    print(res.status, res.read().decode())", e.targets[0].url))
	case db != "":
		e.Kind = KindDatabase
		if db == "redis" || db == "memcached" {
			e.Kind = KindCache
		}
		e.Summary = "A " + db + " Service of your cluster, forwarded to your computer."
		hp := 0
		for _, f := range fs {
			if kubeDBPorts[f.Port] == db {
				hp = f.HostPort
			}
		}
		e.field("Host", "127.0.0.1", "")
		e.field("Port", strconv.Itoa(hp), "")
		if c := kubeDBClient(db, hp); c != "" {
			e.snippet(strings.Fields(c)[0], "bash", c)
		}
	default:
		e.Kind = KindGeneric
		e.Summary = "A Service of your cluster, forwarded to your computer."
	}
	e.snippet("kubectl", "bash", kc+" -n "+shq(f0.Namespace)+" describe service "+shq(f0.Service)+"\n"+kc+" -n "+shq(f0.Namespace)+" get endpointslices -l kubernetes.io/service-name="+shq(f0.Service))
	return e
}

func kubeDBClient(kind string, port int) string {
	p := strconv.Itoa(port)
	switch kind {
	case "postgres":
		return "psql -h 127.0.0.1 -p " + p + " -U postgres"
	case "mysql":
		return "mysql -h 127.0.0.1 -P " + p + " -u root -p"
	case "mongo":
		return "mongosh mongodb://127.0.0.1:" + p
	case "redis":
		return "redis-cli -h 127.0.0.1 -p " + p + " ping"
	case "sqlserver":
		return "sqlcmd -S 127.0.0.1," + p + " -U sa"
	}
	return ""
}

func ingressCard(us []model.KubeIngressURL, st model.KubeStatus, kc string) kubeEntry {
	// The root path is the app's front door: list it first so Try and the post-deploy address default to it
	// (a rule like /api usually answers 404 on its bare prefix).
	us = slices.Clone(us)
	slices.SortStableFunc(us, func(a, b model.KubeIngressURL) int {
		root := func(u model.KubeIngressURL) int {
			if u.Path == "" || u.Path == "/" {
				return 0
			}
			return 1
		}
		return root(a) - root(b)
	})
	u0 := us[0]
	label := u0.Ingress
	if u0.Namespace != "default" {
		label = u0.Namespace + "/" + u0.Ingress
	}
	e := newKubeEntry(u0.NodeID, "Ingress", label)
	e.Kind = KindWebService
	e.Summary = "An Ingress of your cluster: the ingress controller routes these URLs to its Services."
	var curls []string
	for _, u := range us {
		backend := u.Service
		if u.Port != "" {
			backend += ":" + u.Port
		}
		hint := "→ Service " + backend + "."
		if u.Host != "" && u.URL != u.Address {
			hint += " Browsers resolve *.localhost to this computer; other clients call " + u.Address + " with the header Host: " + u.Host + "."
		}
		e.field("URL "+u.Host+u.Path, u.URL, hint)
		e.targets = append(e.targets, kubeTarget{url: u.URL, address: u.Address, host: u.Host})
		e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: http.MethodGet, Path: u.Path, URL: u.URL, Description: u.Host + u.Path + " → " + backend})
		c := "curl -i " + shq(u.Address)
		if u.Host != "" {
			c = "curl -i -H " + shq("Host: "+u.Host) + " " + shq(u.Address)
		}
		curls = append(curls, c)
		if u.Class != "" && u.Class != st.Cluster.IngressClass && u.Class != "nginx" {
			e.Warning = "ingressClassName " + u.Class + " has no controller in the local cluster (it serves " + st.Cluster.IngressClass + " and nginx): these URLs answer 404 unless the workflow installs that controller."
		}
	}
	e.Actions = append(e.Actions, model.TryAction{ID: ActRequest, Label: "Send a request", Description: "Calls the URL through the cluster's ingress controller; change the path if you like.", Input: "http"})
	e.snippet("curl", "bash", strings.Join(curls, "\n\n"))
	e.snippet("kubectl", "bash", kc+" -n "+shq(u0.Namespace)+" describe ingress "+shq(u0.Ingress))
	return e
}

// TryKube runs a TryRequest against a Service or Ingress card: only
// "request", to the forwarded ports or the ingress port on this machine.
func TryKube(ctx context.Context, st model.KubeStatus, req model.TryRequest) (model.TryResult, error) {
	entries := kubeEntries(st)
	i := slices.IndexFunc(entries, func(e kubeEntry) bool { return e.NodeID != "" && (e.NodeID == req.NodeID || e.Address == req.NodeID) })
	if i < 0 {
		return model.TryResult{}, ErrNotFound
	}
	e := entries[i]
	if req.Action != ActRequest || len(e.targets) == 0 {
		return model.TryResult{}, badRequest("action %q is not available for %s", req.Action, e.Label)
	}
	req.Method = strings.ToUpper(strings.TrimSpace(req.Method))
	if req.Method == "" || req.Method == "ANY" {
		req.Method = http.MethodGet
	}
	if !validMethod[req.Method] {
		return model.TryResult{}, badRequest("unsupported method %q", req.Method)
	}
	t := e.targets[0]
	target := strings.TrimSpace(req.Path)
	host := t.host
	switch {
	case target == "":
		target = t.address
	case !strings.Contains(target, "://"):
		target = strings.TrimSuffix(t.address, "/") + "/" + strings.TrimPrefix(target, "/")
	default:
		// A full URL: a rule's *.localhost host is sent as the Host header
		// to 127.0.0.1.
		if u, err := url.Parse(target); err == nil && u.Host != "" && !loopback(u.Hostname()) {
			host = u.Hostname()
			u.Host = "127.0.0.1:" + u.Port()
			target = u.String()
		}
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return model.TryResult{}, badRequest("invalid URL %q", target)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || !loopback(u.Hostname()) || !kubePort(st, u.Port()) {
		return model.TryResult{}, badRequest("Try only calls the forwarded Service ports and the ingress port of this cluster")
	}
	headers := req.Headers
	if host != "" && !headerSet(headers, "Host") {
		headers = map[string]string{}
		for k, v := range req.Headers {
			headers[k] = v
		}
		headers["Host"] = host
	}
	return doHTTP(ctx, req.Method, u.String(), headers, req.Input), nil
}

func kubePort(st model.KubeStatus, port string) bool {
	p, err := strconv.Atoi(port)
	if err != nil || p == 0 {
		return false
	}
	if p == st.Cluster.IngressPort {
		return true
	}
	return slices.ContainsFunc(st.Forwards, func(f model.KubeForward) bool { return f.HostPort == p })
}

// PostmanKube is a collection with a GET per forwarded HTTP port and per
// Ingress URL (with its Host header).
func PostmanKube(wf *model.Workflow, st model.KubeStatus) PostmanCollection {
	name := st.WorkflowID
	if wf != nil && wf.Name != "" {
		name = wf.Name
	}
	col := PostmanCollection{
		Info:     PostmanInfo{Name: name + " (Nimbo Kubernetes)", Description: "Requests to the Services Nimbo forwards and the Ingress URLs of the local cluster.", Schema: PostmanSchema},
		Item:     []PostmanItem{},
		Variable: []PostmanVariable{},
	}
	for _, e := range kubeEntries(st) {
		var items []PostmanItem
		for _, t := range e.targets {
			u, err := url.Parse(t.address)
			if err != nil {
				continue
			}
			hdr := []PostmanHeader{}
			if t.host != "" {
				hdr = append(hdr, PostmanHeader{"Host", t.host})
			}
			items = append(items, PostmanItem{Name: "GET " + t.url, Request: &PostmanRequest{
				Method: http.MethodGet, Header: hdr,
				URL: PostmanURL{Raw: t.address, Protocol: u.Scheme, Host: strings.Split(u.Hostname(), "."), Port: u.Port(), Path: strings.Split(strings.TrimPrefix(u.Path, "/"), "/")},
			}})
		}
		if len(items) > 0 {
			col.Item = append(col.Item, PostmanItem{Name: e.Label, Item: items})
		}
	}
	return col
}
