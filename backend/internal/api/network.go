package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/netcheck"
)

// networkAnalysis analyzes wf against the sandbox's deployed state (the
// cached `terraform show -json`; empty when nothing is deployed).
func (s *Server) networkAnalysis(r *http.Request) (*model.Workflow, *netcheck.Analysis, error) {
	wf, err := s.cfg.Store.Get(r.PathValue("id"))
	if err != nil {
		return nil, nil, err
	}
	hclcodec.Normalize(wf)
	st, err := s.cfg.Sandbox.State(r.Context(), wf)
	if err != nil {
		s.log.Debug("network: deployed state unavailable, analyzing the design", "workflow", wf.ID, "err", err)
		st = nil
	}
	return wf, netcheck.Analyze(wf, st), nil
}

func (s *Server) getNetwork(w http.ResponseWriter, r *http.Request) error {
	_, a, err := s.networkAnalysis(r)
	if err != nil {
		return err
	}
	return ok(w, a.Summary())
}

func (s *Server) networkReachability(w http.ResponseWriter, r *http.Request) error {
	var req model.ReachabilityRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	_, a, err := s.networkAnalysis(r)
	if err != nil {
		return err
	}
	res, err := a.Reachability(req)
	if errors.Is(err, netcheck.ErrBadRequest) {
		return errorf(http.StatusBadRequest, "%s", strings.TrimPrefix(err.Error(), netcheck.ErrBadRequest.Error()+": "))
	}
	if err != nil {
		return err
	}
	if req.Live {
		a.Live(r.Context(), s.cfg.Sandbox, req, &res)
	}
	return ok(w, res)
}

// testInstance adds a small, clearly labelled EC2 instance serving a page
// on port 80 (plus its security group) into a subnet, so beginners can see
// traffic really flow after deploying.
func (s *Server) addTestInstance(w http.ResponseWriter, r *http.Request) error {
	var req model.TestInstanceRequest
	if err := decode(w, r, maxBody, &req); err != nil {
		return err
	}
	if req.Subnet == "" {
		return errorf(http.StatusBadRequest, "subnet is required")
	}
	var nodeID string
	wf, err := s.mutate(r.PathValue("id"), func(wf *model.Workflow) ([]model.Diagnostic, error) {
		var sub *model.Node
		ref := req.Subnet
		for i := range wf.Nodes {
			n := &wf.Nodes[i]
			if n.Type != "aws_subnet" || n.Kind != model.KindResource {
				continue
			}
			addr := hclcodec.Address(n)
			if n.ID == ref || addr == ref {
				sub, ref = n, addr
			} else if strings.HasPrefix(ref, addr+"[") {
				sub = n
			}
		}
		if sub == nil {
			return nil, errorf(http.StatusBadRequest, "%q is not an aws_subnet of the workflow", req.Subnet)
		}
		if ref == hclcodec.Address(sub) && hasMeta(sub, "for_each", "count") {
			return nil, errorf(http.StatusBadRequest, "%s has several instances: pick one, e.g. %s[\"a\"] or %s[0]", ref, ref, ref)
		}
		vpcExpr := ""
		for _, a := range sub.Body.Attrs {
			if a.Name == "vpc_id" {
				vpcExpr = a.Value.Expr
			}
		}
		if vpcExpr == "" || strings.Contains(vpcExpr, "each.") || strings.Contains(vpcExpr, "count.") {
			vpcExpr = ref + ".vpc_id"
		}
		taken := map[string]bool{}
		for _, n := range wf.Nodes {
			taken[hclcodec.Address(&n)] = true
		}
		name := "test_web"
		for i := 2; taken["aws_instance."+name] || taken["aws_security_group."+name]; i++ {
			name = "test_web_" + strconv.Itoa(i)
		}
		files := map[string][]byte{"main.tf": []byte(testInstanceHCL(name, ref, vpcExpr))}
		part, diags, err := hclcodec.Import("test-instance", files)
		if err != nil {
			return nil, err
		}
		for _, d := range diags {
			if d.Severity == model.SeverityError {
				return nil, fmt.Errorf("test instance template: %s", d.Message)
			}
		}
		before := nodeIDs(wf)
		x, y := sub.Position.X, sub.Position.Y+cardHeight+cardGap
		for i, n := range part.Nodes {
			n.ID = ""
			n.File = ""
			n.Order = 0
			n.Position = model.Position{X: x + float64(i)*(cardWidth+cardGap), Y: y}
			n.Positioned = sub.Positioned
			n.Label = map[string]string{"aws_instance": "Test web server", "aws_security_group": "Test web server: allow HTTP"}[n.Type]
			wf.Nodes = append(wf.Nodes, n)
		}
		hclcodec.Normalize(wf)
		settleNewNodes(wf, before)
		for _, n := range wf.Nodes {
			if n.Type == "aws_instance" && n.Name == name {
				nodeID = n.ID
			}
		}
		return nil, nil
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, map[string]any{"workflow": wf, "nodeId": nodeID})
}

func hasMeta(n *model.Node, names ...string) bool {
	for _, a := range n.Body.Attrs {
		for _, m := range names {
			if a.Name == m {
				return true
			}
		}
	}
	return false
}

// testInstanceHCL is the test instance: Amazon Linux 2023 (the local
// emulator maps the AMI name to its image), t3.micro and a user_data web
// server bound to the instance's own address (inside the emulator 0.0.0.0:80
// is taken by the metadata proxy; on AWS binding the private IP works too).
// No associate_public_ip_address: the subnet decides (and the emulator
// drops the security groups of instances that set it).
func testInstanceHCL(name, subnet, vpcExpr string) string {
	return `# Test instance added by Nimbo's network checks: a tiny web server on port 80
# so you can check that traffic really flows. Delete it when you're done.
resource "aws_security_group" "` + name + `" {
  name_prefix = "nimbo-test-web-"
  description = "Nimbo test web server: HTTP from anywhere"
  vpc_id      = ` + vpcExpr + `

  # Port 80 open to the whole internet (0.0.0.0/0) so your browser can load
  # the test page. To close it, replace this block with "ingress = []"
  # (deleting the block alone keeps the deployed rule), deploy, and check
  # reachability again.
  ingress {
    description = "HTTP from anywhere"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Let the instance start connections anywhere (updates, calls to APIs).
  egress {
    description = "All outbound traffic"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_instance" "` + name + `" {
  # "ami-amazonlinux2023" is the local sandbox's Amazon Linux 2023 image. On a
  # real AWS account use a real AMI ID (e.g. from data "aws_ami").
  ami                         = "ami-amazonlinux2023"
  instance_type               = "t3.micro"
  subnet_id                   = ` + subnet + `.id
  vpc_security_group_ids      = [aws_security_group.` + name + `.id]
  user_data_replace_on_change = true

  # Serves a friendly page on port 80, bound to the instance's own address.
  user_data = <<-EOT
    #!/bin/sh
    IP=$(curl -s http://169.254.169.254/latest/meta-data/local-ipv4)
    mkdir -p /srv/nimbo
    echo "<h1>Hello from your Nimbo test instance</h1><p>Instance address: $IP. If you can read this, traffic reached it.</p>" > /srv/nimbo/index.html
    cd /srv/nimbo && nohup python3 -m http.server 80 --bind "$IP" >/var/log/nimbo-test-web.log 2>&1 &
  EOT

  tags = {
    Name = "nimbo-test-web"
  }
}
`
}
