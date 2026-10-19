package netcheck

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/catalog"
	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
	"github.com/ismaelosuna7824/nimbo/backend/internal/sandbox"
)

func importFiles(t *testing.T, files map[string][]byte) *model.Workflow {
	t.Helper()
	wf, _, err := hclcodec.Import("test", files)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range hclcodec.Normalize(wf) {
		if d.Severity == model.SeverityError {
			t.Fatalf("normalize: %+v", d)
		}
	}
	return wf
}

func importDir(t *testing.T, dir string, extra map[string]string) *model.Workflow {
	t.Helper()
	files := map[string][]byte{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tf") {
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			files[e.Name()] = b
		}
	}
	for k, v := range extra {
		files[k] = []byte(v)
	}
	return importFiles(t, files)
}

func vpcBasics(t *testing.T, extra string) *model.Workflow {
	t.Helper()
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	files, ok := cat.TemplateFiles("aws-vpc-basics")
	if !ok {
		t.Fatal("template aws-vpc-basics missing")
	}
	if extra != "" {
		files["extra.tf"] = []byte(extra)
	}
	return importFiles(t, files)
}

func nodeID(t *testing.T, wf *model.Workflow, addr string) string {
	t.Helper()
	for _, n := range wf.Nodes {
		if hclcodec.Address(&n) == addr {
			return n.ID
		}
	}
	t.Fatalf("no node %s", addr)
	return ""
}

func reach(t *testing.T, a *Analysis, wf *model.Workflow, from, to, proto string, port int) model.ReachabilityResult {
	t.Helper()
	ep := func(s string) model.NetEndpoint {
		if s == "internet" {
			return model.NetEndpoint{Kind: "internet"}
		}
		return model.NetEndpoint{Kind: "node", NodeID: nodeID(t, wf, s)}
	}
	res, err := a.Reachability(model.ReachabilityRequest{Source: ep(from), Destination: ep(to), Protocol: proto, Port: port})
	if err != nil {
		t.Fatalf("%s -> %s: %v", from, to, err)
	}
	return res
}

func errorsOf(diags []model.Diagnostic) []model.Diagnostic {
	var out []model.Diagnostic
	for _, d := range diags {
		if d.Severity == model.SeverityError {
			out = append(out, d)
		}
	}
	return out
}

func codes(diags []model.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return out
}

func pathString(res model.ReachabilityResult) string {
	var b strings.Builder
	for _, h := range res.Path {
		b.WriteString("  " + h.Verdict + " " + h.Component + ": " + h.Reason + "\n")
	}
	return b.String()
}

const webInstance = `
resource "aws_instance" "web" {
  ami                    = "ami-amazonlinux2023"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.public["a"].id
  vpc_security_group_ids = [aws_security_group.web.id]
}
`

func TestVPCBasicsTemplate(t *testing.T) {
	wf := vpcBasics(t, "")
	a := Analyze(wf, nil)
	if errs := errorsOf(a.Findings()); len(errs) > 0 {
		t.Fatalf("template has error findings: %+v", errs)
	}
	if len(a.Findings()) != 0 {
		t.Errorf("findings on the template: %+v", a.Findings())
	}
	s := a.Summary()
	if s.Basis != model.NetworkBasisDesign {
		t.Errorf("basis %s", s.Basis)
	}
	if len(s.Subnets) != 2 {
		t.Fatalf("subnets %+v", s.Subnets)
	}
	for _, sn := range s.Subnets {
		if sn.Classification != model.SubnetPublic || sn.Zone == "" || sn.CIDR == "" {
			t.Errorf("subnet %+v", sn)
		}
	}
	if s.Subnets[0].CIDR != "10.0.1.0/24" || s.Subnets[1].Address != `aws_subnet.public["b"]` {
		t.Errorf("subnets %+v", s.Subnets)
	}
	if len(s.SecurityGroups) != 1 || !strings.Contains(s.SecurityGroups[0].Summary, "port 80, port 443 from anywhere") {
		t.Errorf("security groups %+v", s.SecurityGroups)
	}
}

func TestVPCBasicsReachability(t *testing.T) {
	wf := vpcBasics(t, webInstance)
	a := Analyze(wf, nil)
	if errs := errorsOf(a.Findings()); len(errs) > 0 {
		t.Fatalf("error findings: %+v", errs)
	}
	ok := reach(t, a, wf, "internet", "aws_instance.web", "tcp", 80)
	if !ok.Reachable {
		t.Errorf("internet -> web:80 should be reachable:\n%s", pathString(ok))
	}
	ssh := reach(t, a, wf, "internet", "aws_instance.web", "tcp", 22)
	if ssh.Reachable || ssh.Verdict != model.ReachNo {
		t.Fatalf("internet -> web:22 should be blocked:\n%s", pathString(ssh))
	}
	if len(ssh.Blockers) != 1 || !strings.Contains(ssh.Blockers[0], "Security group aws_security_group.web") {
		t.Errorf("blockers %v", ssh.Blockers)
	}
	if len(ssh.Suggestions) == 0 || !strings.Contains(ssh.Suggestions[0], "port 22") {
		t.Errorf("suggestions %v", ssh.Suggestions)
	}
	// The SG decides: open 22 and it becomes reachable.
	wf2 := vpcBasics(t, webInstance+`
resource "aws_vpc_security_group_ingress_rule" "ssh" {
  security_group_id = aws_security_group.web.id
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
  cidr_ipv4         = "0.0.0.0/0"
}`)
	if res := reach(t, Analyze(wf2, nil), wf2, "internet", "aws_instance.web", "tcp", 22); !res.Reachable {
		t.Errorf("with the SSH rule, internet -> web:22 should be reachable:\n%s", pathString(res))
	}
	out := reach(t, a, wf, "aws_instance.web", "internet", "tcp", 443)
	if !out.Reachable {
		t.Errorf("web -> internet:443 should be reachable:\n%s", pathString(out))
	}
}

func TestThreeTier(t *testing.T) {
	wf := importDir(t, "testdata/three-tier", nil)
	a := Analyze(wf, nil)
	if len(a.Findings()) != 0 {
		t.Errorf("findings: %+v", a.Findings())
	}
	s := a.Summary()
	classes := map[string]string{}
	for _, sn := range s.Subnets {
		classes[sn.Address] = sn.Classification
	}
	want := map[string]string{
		`aws_subnet.this["public-0"]`: model.SubnetPublic, `aws_subnet.this["public-1"]`: model.SubnetPublic,
		`aws_subnet.this["app-0"]`: model.SubnetPrivate, `aws_subnet.this["app-1"]`: model.SubnetPrivate,
		`aws_subnet.this["db-0"]`: model.SubnetIsolated, `aws_subnet.this["db-1"]`: model.SubnetIsolated,
	}
	for k, v := range want {
		if classes[k] != v {
			t.Errorf("%s: %s, want %s (all: %v)", k, classes[k], v, classes)
		}
	}
	matrix := []struct {
		from, to string
		port     int
		verdict  string
		blocker  string
	}{
		{"internet", "aws_lb.web", 443, model.ReachYes, ""},
		{"internet", "aws_lb.web", 80, model.ReachNo, "Listener :80"},
		{"internet", "aws_instance.app", 8080, model.ReachNo, "no public address"},
		{"aws_instance.app", "aws_db_instance.db", 5432, model.ReachYes, ""},
		{"aws_instance.app", "aws_db_instance.db", 3306, model.ReachNo, ""},
		{"aws_instance.app", "internet", 443, model.ReachYes, ""},
		{"aws_db_instance.db", "internet", 443, model.ReachNo, "isolated"},
		{"aws_lb.web", "aws_db_instance.db", 5432, model.ReachNo, "aws_security_group.alb"},
		{"internet", "aws_db_instance.db", 5432, model.ReachNo, "no public address"},
	}
	for _, m := range matrix {
		res := reach(t, a, wf, m.from, m.to, "tcp", m.port)
		if res.Verdict != m.verdict {
			t.Errorf("%s -> %s:%d = %s, want %s\n%s", m.from, m.to, m.port, res.Verdict, m.verdict, pathString(res))
			continue
		}
		if m.blocker != "" && !strings.Contains(strings.ToLower(strings.Join(res.Blockers, "\n")), strings.ToLower(m.blocker)) {
			t.Errorf("%s -> %s:%d blockers %v, want one mentioning %q", m.from, m.to, m.port, res.Blockers, m.blocker)
		}
	}
	// The ALB path follows the listener to the app instance.
	res := reach(t, a, wf, "internet", "aws_lb.web", "tcp", 443)
	var comps []string
	for _, h := range res.Path {
		comps = append(comps, h.Component)
	}
	joined := strings.Join(comps, " | ")
	for _, want := range []string{"Internet gateway aws_internet_gateway.main", "Listener :443", "Target group aws_lb_target_group.app", "Security group aws_security_group.app"} {
		if !strings.Contains(joined, want) {
			t.Errorf("path lacks %q: %s", want, joined)
		}
	}
	for _, h := range res.Path {
		if h.Component == "Internet gateway aws_internet_gateway.main" && h.NodeID != nodeID(t, wf, "aws_internet_gateway.main") {
			t.Errorf("hop node %q", h.NodeID)
		}
	}
	// Explanations are specific.
	app := reach(t, a, wf, "internet", "aws_instance.app", "tcp", 8080)
	if !strings.Contains(strings.Join(app.Blockers, " "), "map_public_ip_on_launch is false") {
		t.Errorf("app blockers %v", app.Blockers)
	}
	if !strings.Contains(strings.Join(app.Blockers, " "), "NAT gateway") {
		t.Errorf("app blockers should explain the NAT route: %v", app.Blockers)
	}
}

const basicVPC = `
resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
}
resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id
}
resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }
}
resource "aws_subnet" "public_a" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.1.0/24"
  availability_zone       = "us-east-1a"
  map_public_ip_on_launch = true
}
resource "aws_subnet" "public_b" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.2.0/24"
  availability_zone       = "us-east-1b"
  map_public_ip_on_launch = true
}
resource "aws_route_table_association" "a" {
  subnet_id      = aws_subnet.public_a.id
  route_table_id = aws_route_table.public.id
}
resource "aws_route_table_association" "b" {
  subnet_id      = aws_subnet.public_b.id
  route_table_id = aws_route_table.public.id
}
`

func TestChecks(t *testing.T) {
	cases := []struct {
		name  string
		hcl   string
		code  string
		sev   model.Severity
		node  string
		clean bool
	}{
		{name: "baseline is clean", hcl: basicVPC, clean: true},
		{name: "subnet outside VPC", code: "net_subnet_outside_network", sev: model.SeverityError, node: "aws_subnet.x", hcl: basicVPC + `
resource "aws_subnet" "x" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.1.0.0/24"
}`},
		{name: "overlapping subnets", code: "net_subnet_overlap", sev: model.SeverityError, node: "aws_subnet.x", hcl: basicVPC + `
resource "aws_subnet" "x" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.1.128/25"
}`},
		{name: "overlap via cidrsubnet", code: "net_subnet_overlap", sev: model.SeverityError, hcl: `
resource "aws_vpc" "v" {
  cidr_block = "10.9.0.0/16"
}
resource "aws_subnet" "s" {
  count      = 2
  vpc_id     = aws_vpc.v.id
  cidr_block = cidrsubnet(aws_vpc.v.cidr_block, 8, 0)
}`},
		{name: "peered VPCs overlap", code: "net_peering_overlap", sev: model.SeverityError, node: "aws_vpc_peering_connection.p", hcl: basicVPC + `
resource "aws_vpc" "other" {
  cidr_block = "10.0.128.0/17"
}
resource "aws_vpc_peering_connection" "p" {
  vpc_id      = aws_vpc.main.id
  peer_vpc_id = aws_vpc.other.id
}`},
		{name: "public subnet without IGW route", code: "net_public_subnet_no_igw", sev: model.SeverityWarning, node: "aws_subnet.x", hcl: basicVPC + `
resource "aws_subnet" "x" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.0.9.0/24"
  map_public_ip_on_launch = true
}`},
		{name: "private-named subnet routes to IGW", code: "net_private_subnet_igw", sev: model.SeverityInfo, node: "aws_subnet.private", hcl: basicVPC + `
resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.9.0/24"
}
resource "aws_route_table_association" "p" {
  subnet_id      = aws_subnet.private.id
  route_table_id = aws_route_table.public.id
}`},
		{name: "lambda in private subnet without egress", code: "net_no_egress", sev: model.SeverityWarning, node: "aws_lambda_function.f", hcl: basicVPC + `
resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.9.0/24"
}
resource "aws_security_group" "f" {
  vpc_id = aws_vpc.main.id
}
resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "arn:aws:iam::123456789012:role/x"
  runtime       = "nodejs20.x"
  handler       = "index.handler"
  filename      = "f.zip"
  vpc_config {
    subnet_ids         = [aws_subnet.private.id]
    security_group_ids = [aws_security_group.f.id]
  }
}`},
		{name: "lambda with an s3 endpoint is fine", clean: true, hcl: basicVPC + `
resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.9.0/24"
}
resource "aws_vpc_endpoint" "s3" {
  vpc_id       = aws_vpc.main.id
  service_name = "com.amazonaws.us-east-1.s3"
}
resource "aws_security_group" "f" {
  vpc_id = aws_vpc.main.id
}
resource "aws_lambda_function" "f" {
  function_name = "f"
  role          = "arn:aws:iam::123456789012:role/x"
  runtime       = "nodejs20.x"
  handler       = "index.handler"
  filename      = "f.zip"
  vpc_config {
    subnet_ids         = [aws_subnet.private.id]
    security_group_ids = [aws_security_group.f.id]
  }
}`},
		{name: "NAT gateway in private subnet", code: "net_nat_no_igw", sev: model.SeverityWarning, node: "aws_nat_gateway.n", hcl: basicVPC + `
resource "aws_subnet" "private" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.0.9.0/24"
}
resource "aws_eip" "n" {
  domain = "vpc"
}
resource "aws_nat_gateway" "n" {
  allocation_id = aws_eip.n.id
  subnet_id     = aws_subnet.private.id
}`},
		{name: "route table not associated", code: "net_route_table_unassociated", sev: model.SeverityWarning, node: "aws_route_table.lonely", hcl: basicVPC + `
resource "aws_route_table" "lonely" {
  vpc_id = aws_vpc.main.id
}`},
		{name: "route to another VPC's gateway", code: "net_route_cross_vpc", sev: model.SeverityError, node: "aws_route.bad", hcl: basicVPC + `
resource "aws_vpc" "other" {
  cidr_block = "10.1.0.0/16"
}
resource "aws_internet_gateway" "other" {
  vpc_id = aws_vpc.other.id
}
resource "aws_route" "bad" {
  route_table_id         = aws_route_table.public.id
  destination_cidr_block = "192.0.2.0/24"
  gateway_id             = aws_internet_gateway.other.id
}`},
		{name: "security group across VPCs", code: "net_sg_cross_vpc", sev: model.SeverityError, node: "aws_security_group_rule.r", hcl: basicVPC + `
resource "aws_vpc" "other" {
  cidr_block = "10.1.0.0/16"
}
resource "aws_security_group" "a" {
  vpc_id = aws_vpc.main.id
}
resource "aws_security_group" "b" {
  vpc_id = aws_vpc.other.id
}
resource "aws_security_group_rule" "r" {
  type                     = "ingress"
  security_group_id        = aws_security_group.a.id
  source_security_group_id = aws_security_group.b.id
  from_port                = 443
  to_port                  = 443
  protocol                 = "tcp"
}`},
		{name: "instance with an SG of another VPC", code: "net_sg_cross_vpc", sev: model.SeverityError, node: "aws_instance.i", hcl: basicVPC + `
resource "aws_vpc" "other" {
  cidr_block = "10.1.0.0/16"
}
resource "aws_security_group" "b" {
  vpc_id = aws_vpc.other.id
}
resource "aws_instance" "i" {
  ami                    = "ami-1"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.public_a.id
  vpc_security_group_ids = [aws_security_group.b.id]
}`},
		{name: "ALB in one AZ", code: "net_single_az", sev: model.SeverityError, node: "aws_lb.l", hcl: basicVPC + `
resource "aws_lb" "l" {
  name    = "l"
  subnets = [aws_subnet.public_a.id]
}`},
		{name: "ALB in two AZs is fine", clean: true, hcl: basicVPC + `
resource "aws_lb" "l" {
  name    = "l"
  subnets = [aws_subnet.public_a.id, aws_subnet.public_b.id]
}`},
		{name: "NLB in one AZ is allowed", clean: true, hcl: basicVPC + `
resource "aws_lb" "l" {
  name               = "l"
  load_balancer_type = "network"
  subnets            = [aws_subnet.public_a.id]
}`},
		{name: "DB subnet group in one AZ", code: "net_single_az", sev: model.SeverityError, node: "aws_db_subnet_group.g", hcl: basicVPC + `
resource "aws_db_subnet_group" "g" {
  name       = "g"
  subnet_ids = [aws_subnet.public_a.id]
}`},
		{name: "unknown values are skipped", clean: true, hcl: `
data "aws_vpc" "existing" {
  default = true
}
variable "cidr" {
  type = string
}
resource "aws_subnet" "s" {
  vpc_id     = data.aws_vpc.existing.id
  cidr_block = var.cidr
}
resource "aws_subnet" "t" {
  vpc_id     = data.aws_vpc.existing.id
  cidr_block = var.cidr
}
resource "aws_lb" "l" {
  name    = "l"
  subnets = [aws_subnet.s.id, aws_subnet.t.id]
}`},
		{name: "azure subnet outside vnet", code: "net_subnet_outside_network", sev: model.SeverityError, node: "azurerm_subnet.s", hcl: `
resource "azurerm_virtual_network" "v" {
  name                = "v"
  location            = "westeurope"
  resource_group_name = "rg"
  address_space       = ["10.0.0.0/16"]
}
resource "azurerm_subnet" "s" {
  name                 = "s"
  resource_group_name  = "rg"
  virtual_network_name = azurerm_virtual_network.v.name
  address_prefixes     = ["10.5.1.0/24"]
}`},
		{name: "azure duplicate NSG priority", code: "net_rule_priority_duplicate", sev: model.SeverityError, node: "azurerm_network_security_group.n", hcl: `
resource "azurerm_network_security_group" "n" {
  name                = "n"
  location            = "westeurope"
  resource_group_name = "rg"
  security_rule {
    name                       = "a"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "80"
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }
  security_rule {
    name                       = "b"
    priority                   = 100
    direction                  = "Inbound"
    access                     = "Allow"
    protocol                   = "Tcp"
    source_port_range          = "*"
    destination_port_range     = "443"
    source_address_prefix      = "*"
    destination_address_prefix = "*"
  }
}`},
		{name: "gcp overlapping ranges", code: "net_subnet_overlap", sev: model.SeverityError, node: "google_compute_subnetwork.b", hcl: `
resource "google_compute_network" "n" {
  name                    = "n"
  auto_create_subnetworks = false
}
resource "google_compute_subnetwork" "a" {
  name          = "a"
  network       = google_compute_network.n.id
  ip_cidr_range = "10.0.0.0/20"
  region        = "us-central1"
}
resource "google_compute_subnetwork" "b" {
  name          = "b"
  network       = google_compute_network.n.self_link
  ip_cidr_range = "10.0.8.0/24"
  region        = "us-central1"
}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wf := importFiles(t, map[string][]byte{"main.tf": []byte(c.hcl)})
			got := Analyze(wf, nil).Findings()
			if c.clean {
				if len(got) != 0 {
					t.Fatalf("want no findings, got %+v", got)
				}
				return
			}
			var hit *model.Diagnostic
			for i := range got {
				if got[i].Code == c.code {
					hit = &got[i]
				}
			}
			if hit == nil {
				t.Fatalf("want %s, got %v (%+v)", c.code, codes(got), got)
			}
			if hit.Severity != c.sev || hit.Detail == "" || hit.Fix == "" {
				t.Errorf("finding %+v", hit)
			}
			if c.node != "" && hit.NodeID != nodeID(t, wf, c.node) {
				t.Errorf("finding on node %q, want %s", hit.NodeID, c.node)
			}
			for _, d := range got {
				if !IsFinding(d) {
					t.Errorf("non-net code %s", d.Code)
				}
			}
		})
	}
}

func TestNACLEphemeral(t *testing.T) {
	nacl := func(egressPorts string) string {
		return basicVPC + `
resource "aws_security_group" "web" {
  vpc_id = aws_vpc.main.id
  ingress {
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }
}
resource "aws_instance" "web" {
  ami                    = "ami-1"
  instance_type          = "t3.micro"
  subnet_id              = aws_subnet.public_a.id
  vpc_security_group_ids = [aws_security_group.web.id]
}
resource "aws_network_acl" "public" {
  vpc_id     = aws_vpc.main.id
  subnet_ids = [aws_subnet.public_a.id]
  ingress {
    rule_no    = 100
    action     = "allow"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    from_port  = 443
    to_port    = 443
  }
  egress {
    rule_no    = 100
    action     = "allow"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    ` + egressPorts + `
  }
}`
	}
	broken := importFiles(t, map[string][]byte{"main.tf": []byte(nacl("from_port = 443\n    to_port = 443"))})
	a := Analyze(broken, nil)
	if !slices.Contains(codes(a.Findings()), "net_nacl_ephemeral") {
		t.Fatalf("want net_nacl_ephemeral, got %+v", a.Findings())
	}
	res := reach(t, a, broken, "internet", "aws_instance.web", "tcp", 443)
	if res.Reachable {
		t.Fatalf("replies are dropped, want unreachable:\n%s", pathString(res))
	}
	if !strings.Contains(strings.Join(res.Blockers, " "), "ephemeral") {
		t.Errorf("blockers %v", res.Blockers)
	}
	fixed := importFiles(t, map[string][]byte{"main.tf": []byte(nacl("from_port = 1024\n    to_port = 65535"))})
	b := Analyze(fixed, nil)
	if slices.Contains(codes(b.Findings()), "net_nacl_ephemeral") {
		t.Errorf("fixed NACL still flagged: %+v", b.Findings())
	}
	if res := reach(t, b, fixed, "internet", "aws_instance.web", "tcp", 443); !res.Reachable {
		t.Errorf("fixed NACL should pass:\n%s", pathString(res))
	}
	// Inbound denied by rule order: a deny before the allow wins.
	denyFirst := importFiles(t, map[string][]byte{"main.tf": []byte(strings.Replace(nacl("from_port = 1024\n    to_port = 65535"), `  ingress {
    rule_no    = 100`, `  ingress {
    rule_no    = 50
    action     = "deny"
    protocol   = "tcp"
    cidr_block = "0.0.0.0/0"
    from_port  = 443
    to_port    = 443
  }
  ingress {
    rule_no    = 100`, 1))})
	if res := reach(t, Analyze(denyFirst, nil), denyFirst, "internet", "aws_instance.web", "tcp", 443); res.Reachable || !strings.Contains(strings.Join(res.Blockers, " "), "Rule 50 denies") {
		t.Errorf("deny rule 50 should block:\n%s", pathString(res))
	}
}

func TestDeployedBasis(t *testing.T) {
	wf := importDir(t, "testdata/deployed/aws-vpc-basics", nil)
	raw, err := os.ReadFile("testdata/deployed/aws-vpc-basics.show.json")
	if err != nil {
		t.Fatal(err)
	}
	st, err := sandbox.ParseState(raw, wf)
	if err != nil {
		t.Fatal(err)
	}
	a := Analyze(wf, st)
	s := a.Summary()
	if s.Basis != model.NetworkBasisDeployed {
		t.Fatalf("basis %s: %s", s.Basis, s.BasisReason)
	}
	if len(s.Networks) != 1 || s.Networks[0].ID == "" || !strings.HasPrefix(s.Networks[0].ID, "vpc-") {
		t.Errorf("networks %+v", s.Networks)
	}
	for _, sn := range s.Subnets {
		if !strings.HasPrefix(sn.ID, "subnet-") || sn.Classification != model.SubnetPublic {
			t.Errorf("subnet %+v", sn)
		}
	}
	if errs := errorsOf(a.Findings()); len(errs) > 0 {
		t.Errorf("error findings: %+v", errs)
	}
	res := reach(t, a, wf, "internet", "aws_instance.web", "tcp", 80)
	if !res.Reachable || res.Basis != model.NetworkBasisDeployed {
		t.Errorf("internet -> web:80 on the deployed state:\n%s", pathString(res))
	}
	if res := reach(t, a, wf, "internet", "aws_instance.web", "tcp", 22); res.Reachable {
		t.Errorf("internet -> web:22 should be blocked:\n%s", pathString(res))
	}
	// A design change not deployed yet falls back to the design.
	wf.Nodes = append(wf.Nodes, model.Node{ID: "extra", Kind: model.KindResource, Type: "aws_subnet", Name: "extra",
		Body: model.Body{Attrs: []model.Attr{{Name: "vpc_id", Value: model.Value{Expr: "aws_vpc.main.id"}}, {Name: "cidr_block", Value: model.Value{Expr: `"10.0.9.0/24"`}}}}})
	if b := Analyze(wf, st); b.Basis() != model.NetworkBasisDesign || !strings.Contains(b.Summary().BasisReason, "aws_subnet.extra") {
		t.Errorf("basis %s: %s", b.Basis(), b.Summary().BasisReason)
	}
}

func TestReachabilityRequests(t *testing.T) {
	wf := vpcBasics(t, webInstance+`
resource "aws_s3_bucket" "b" {
  bucket = "b"
}`)
	a := Analyze(wf, nil)
	bad := []model.ReachabilityRequest{
		{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "internet"}, Protocol: "tcp", Port: 80},
		{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: nodeID(t, wf, "aws_instance.web")}, Protocol: "sctp", Port: 80},
		{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: nodeID(t, wf, "aws_instance.web")}, Protocol: "tcp", Port: 0},
		{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: "nope"}, Protocol: "tcp", Port: 80},
		{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: nodeID(t, wf, "aws_route_table.public")}, Protocol: "tcp", Port: 80},
	}
	for i, r := range bad {
		if _, err := a.Reachability(r); err == nil {
			t.Errorf("request %d: want an error", i)
		}
	}
	// AWS services: reachable from a public instance through the IGW.
	res := reach(t, a, wf, "aws_instance.web", "aws_s3_bucket.b", "tcp", 443)
	if !res.Reachable {
		t.Errorf("web -> s3:\n%s", pathString(res))
	}
	// icmp ignores the port
	if res, err := a.Reachability(model.ReachabilityRequest{Source: model.NetEndpoint{Kind: "internet"}, Destination: model.NetEndpoint{Kind: "node", NodeID: "aws_instance.web"}, Protocol: "icmp"}); err != nil || res.Reachable {
		t.Errorf("ping: %v %+v", err, res)
	}
}

func TestCIDRFunctions(t *testing.T) {
	cases := map[string]string{
		`cidrsubnet("10.0.0.0/16", 8, 3)`:                "10.0.3.0/24",
		`cidrsubnet("10.0.0.0/16", 4, 15)`:               "10.0.240.0/20",
		`cidrhost("10.0.1.0/24", 5)`:                     "10.0.1.5",
		`cidrhost("10.0.1.0/24", -1)`:                    "10.0.1.255",
		`cidrnetmask("10.0.0.0/12")`:                     "255.240.0.0",
		`join(",", cidrsubnets("10.0.0.0/16", 8, 8, 4))`: "10.0.0.0/24,10.0.1.0/24,10.0.16.0/20",
		`cidrsubnet("fd00::/56", 8, 2)`:                  "fd00:0:0:2::/64",
	}
	for expr, want := range cases {
		v := evalConst(expr)
		if !v.IsKnown() || v.AsString() != want {
			t.Errorf("%s = %#v, want %s", expr, v, want)
		}
	}
	if v := evalConst(`cidrsubnet("10.0.0.0/24", 8, 300)`); v.IsKnown() {
		t.Errorf("out of range netnum should fail, got %#v", v)
	}
}

func TestTemplatesHaveNoNetworkFindings(t *testing.T) {
	cat, err := catalog.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tpl := range cat.Templates() {
		files, ok := cat.TemplateFiles(tpl.ID)
		if !ok {
			t.Fatalf("template %s has no files", tpl.ID)
		}
		if got := Analyze(importFiles(t, files), nil).Findings(); len(got) != 0 {
			t.Errorf("%s: %+v", tpl.ID, got)
		}
	}
}
