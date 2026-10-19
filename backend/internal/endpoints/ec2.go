package endpoints

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

const (
	instanceSummary    = "A virtual machine running in the local sandbox as a container."
	instanceNotStarted = "Start the sandbox to reach this instance: it runs as a container only while the sandbox runs."
	instanceStopped    = "The instance's container is not running. Start the sandbox (or the instance) to reach it."
	instanceNoPorts    = "Nothing is published on your computer yet: the emulator publishes a port only when a security group allows inbound TCP on it from an address range (e.g. 0.0.0.0/0); port 22 is reachable through the SSH port."
)

// instances describes EC2 instances: Floci runs each as a container whose
// VPC addresses are not reachable from the host; only the SSH port and the
// app ports its security groups open to an address range are published on
// 127.0.0.1.
func (b *builder) instances() {
	for _, r := range b.byType["aws_instance"] {
		id := str(r, "id")
		e := b.add(r, KindInstance, instanceSummary, target{cloud: model.CloudAWS, region: b.regionOf(str(r, "arn")), name: id})
		acc, known := b.sb.Instances[id]
		e.field("Instance ID", id, "")
		e.field("Private IP", str(r, "private_ip"), "The instance's VPC address inside the sandbox: other instances reach it there. Not reachable from your computer.")
		e.field("Public IP", str(r, "public_ip"), "Inside the emulator this is the VPC address; from your computer use the Host URL.")
		e.field("Availability zone", str(r, "availability_zone"), "")
		e.field("Subnet", str(r, "subnet_id"), "")
		switch {
		case !known:
			e.Warning = instanceNotStarted
			continue
		case !acc.Running:
			e.Warning = instanceStopped
			continue
		}
		var curls []string
		for _, p := range acc.AppPorts() {
			u := acc.URL(p)
			e.t.hostURLs = append(e.t.hostURLs, u)
			e.field(fmt.Sprintf("Host URL (port %d)", p), u, "")
			curl := "curl -i " + shq(u)
			if strings.HasPrefix(u, "https:") {
				curl = "curl -ik " + shq(u)
			}
			curls = append(curls, curl)
			if slices.Contains(sandbox.HTTPAppPorts, p) {
				e.Endpoints = append(e.Endpoints, model.HTTPEndpoint{Method: http.MethodGet, Path: "/", URL: u, Description: fmt.Sprintf("Port %d of the instance", p)})
			}
		}
		if ssh, ok := acc.Ports[22]; ok {
			hint := "User ec2-user or root, depending on the AMI; the key is the instance's key pair"
			if k := str(r, "key_name"); k != "" {
				hint += " (" + k + ")"
			}
			e.field("SSH", "127.0.0.1:"+strconv.Itoa(ssh), hint+".")
		}
		if len(curls) > 0 {
			e.snippet("curl", "bash", strings.Join(curls, "\n\n"))
		}
		e.snippet("Open shell", "bash", "docker exec -it "+shq(acc.Container)+" sh")
		switch {
		case len(e.Endpoints) > 0:
			e.t.httpBase = strings.TrimSuffix(e.Endpoints[0].URL, "/")
		case len(e.t.hostURLs) > 0:
			e.t.httpBase = strings.TrimSuffix(e.t.hostURLs[0], "/")
		}
		if e.t.httpBase != "" {
			e.action(model.TryAction{ID: ActRequest, Label: "Send a request", Description: "Calls the instance through its published port; change the path if you like.", Input: "http"})
		}
		if len(e.t.hostURLs) == 0 {
			e.Warning = instanceNoPorts
		}
	}
}

// instancePostman adds an "EC2 instances (sandbox)" folder with a GET per
// published instance port (full host URLs: they are not on the emulator).
func (b *builder) instancePostman(col *PostmanCollection) {
	var items []PostmanItem
	for _, e := range b.entries {
		if e.Kind != KindInstance {
			continue
		}
		for _, raw := range e.t.hostURLs {
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			items = append(items, PostmanItem{Name: "GET " + raw + " · " + e.Label, Request: &PostmanRequest{
				Method: http.MethodGet, Header: []PostmanHeader{},
				URL: PostmanURL{Raw: raw, Protocol: u.Scheme, Host: strings.Split(u.Hostname(), "."), Port: u.Port(), Path: []string{""}},
			}})
		}
	}
	if len(items) > 0 {
		col.Item = append(col.Item, PostmanItem{
			Name:        "EC2 instances (sandbox)",
			Description: "Requests to the app ports the sandbox publishes on your computer for EC2 instances (host ports may change when an instance is replaced).",
			Item:        items,
		})
	}
}

// instancePort reports whether hostPort is a published app port of one of
// the workflow's EC2 instances.
func (b *builder) instancePort(hostPort string) bool {
	for _, a := range b.sb.Instances {
		for p, hp := range a.Ports {
			if p != 22 && strconv.Itoa(hp) == hostPort {
				return true
			}
		}
	}
	return false
}
