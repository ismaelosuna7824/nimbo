package lint

import (
	"strings"
	"testing"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const azsVar = `variable "azs" {
  type        = list(string)
  description = "x"
}
`

// subnets renders an aws_subnet "private" with count and body.
func subnets(count, body string) string {
	return `resource "aws_subnet" "private" {
  count  = ` + count + `
  vpc_id = "vpc-1"
` + body + `
}
`
}

var countOverListCases = []structureCase{
	{name: "variable indexed by count.index", src: azsVar + subnets("length(var.azs)", `  availability_zone = var.azs[count.index]`), want: 1},
	{name: "local in a template", src: `locals { azs = ["us-east-1a", "us-east-1b"] }
` + subnets("length(local.azs)", `  tags = { Name = "app-${local.azs[count.index]}" }`), want: 1},
	{name: "literal list", src: subnets(`length(["us-east-1a", "us-east-1b"])`, `  availability_zone = ["us-east-1a", "us-east-1b"][count.index]`), want: 1},
	{name: "element()", src: azsVar + subnets("length(var.azs)", `  availability_zone = element(var.azs, count.index)`), want: 1},
	{name: "indexed then an attribute", src: `variable "nets" {
  type        = list(object({ az = string }))
  description = "x"
}
` + subnets("length(var.nets)", `  availability_zone = var.nets[count.index].az`), want: 1},
	{name: "spacing doesn't matter", src: azsVar + subnets("length( var.azs )", `  availability_zone = var.azs[ count.index ]`), want: 1},
	{name: "in a nested block", src: azsVar + `resource "aws_instance" "web" {
  count = length(var.azs)
  ami   = "ami-1"
  ebs_block_device {
    device_name       = "/dev/sdb"
    availability_zone = var.azs[count.index]
  }
}`, want: 1},
	{name: "module call", src: `variable "names" {
  type        = list(string)
  description = "x"
}
module "svc" {
  source = "./modules/svc"
  count  = length(var.names)
  name   = var.names[count.index]
}`, want: 1},
	{name: "local module", src: callApp, modules: map[string]string{
		"modules/app": azsVar + subnets("length(var.azs)", `  availability_zone = var.azs[count.index]`) + `output "ids" {
  value       = aws_subnet.private[*].id
  description = "x"
}`,
	}, want: 1},
	{name: "toggle", src: azsVar + `variable "create" {
  type        = bool
  description = "x"
}
` + subnets("var.create ? 1 : 0", `  availability_zone = var.azs[count.index]`), want: 0},
	{name: "numeric count", src: azsVar + subnets("2", `  availability_zone = var.azs[count.index]`), want: 0},
	{name: "for_each", src: azsVar + `resource "aws_subnet" "private" {
  for_each          = toset(var.azs)
  vpc_id            = "vpc-1"
  availability_zone = each.value
}`, want: 0},
	{name: "a different list is indexed", src: azsVar + `variable "names" {
  type        = list(string)
  description = "x"
}
` + subnets("length(var.azs)", `  tags = { Name = var.names[count.index] }`), want: 0},
	{name: "list only counted", src: azsVar + subnets("length(var.azs)", `  cidr_block = cidrsubnet("10.0.0.0/16", 8, count.index)`), want: 0},
	{name: "list indexed by something else", src: azsVar + subnets("length(var.azs)", `  availability_zone = var.azs[0]`), want: 0},
	{name: "resource attribute list", src: `resource "aws_subnet" "a" {
  count             = 2
  vpc_id            = "vpc-1"
  cidr_block        = cidrsubnet("10.0.0.0/16", 8, count.index)
}
resource "aws_instance" "web" {
  count     = length(aws_subnet.a[*].id)
  ami       = "ami-1"
  subnet_id = aws_subnet.a[*].id[count.index]
}`, want: 0},
	{name: "data source", src: `variable "ids" {
  type        = list(string)
  description = "x"
}
data "aws_subnet" "s" {
  count = length(var.ids)
  id    = var.ids[count.index]
}`, want: 0},
}

func init() {
	structureCases["bp_count_over_list"] = append(structureCases["bp_count_over_list"], countOverListCases...)
}

// The fix spells out the moved blocks when the list's elements are known
// distinct strings, and the general form otherwise.
func TestCountOverListMovedBlocks(t *testing.T) {
	exact := func(addr string) string {
		return "moved {\n  from = " + addr + "[0]\n  to   = " + addr + "[\"us-east-1a\"]\n}\nmoved {\n  from = " + addr + "[1]\n  to   = " + addr + "[\"us-east-1b\"]\n}"
	}
	cases := []struct {
		name, src, want string
	}{
		{"local list", `locals { azs = ["us-east-1a", "us-east-1b"] }
` + subnets("length(local.azs)", `  availability_zone = local.azs[count.index]`), exact("aws_subnet.private")},
		{"variable default", `variable "azs" {
  type        = list(string)
  description = "x"
  default     = ["us-east-1a", "us-east-1b"]
}
` + subnets("length(var.azs)", `  availability_zone = var.azs[count.index]`), exact("aws_subnet.private")},
		{"literal list", subnets(`length(["us-east-1a", "us-east-1b"])`, `  availability_zone = ["us-east-1a", "us-east-1b"][count.index]`), exact("aws_subnet.private")},
		{"module call", `locals { azs = ["us-east-1a", "us-east-1b"] }
module "svc" {
  source = "./modules/svc"
  count  = length(local.azs)
  az     = local.azs[count.index]
}`, exact("module.svc")},
		{"unknown elements", azsVar + subnets("length(var.azs)", `  availability_zone = var.azs[count.index]`), `moved { from = aws_subnet.private[<index>] to = aws_subnet.private["<element at that index>"] }`},
		{"duplicate elements", `locals { azs = ["us-east-1a", "us-east-1a"] }
` + subnets("length(local.azs)", `  availability_zone = local.azs[count.index]`), `[<index>]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findings(t, tc.src, "bp_count_over_list")
			if len(got) != 1 {
				t.Fatalf("%d findings, want 1: %+v", len(got), got)
			}
			d := got[0]
			if !strings.Contains(d.Fix, tc.want) {
				t.Errorf("fix lacks %q:\n%s", tc.want, d.Fix)
			}
			if !strings.Contains(d.Fix, "for_each = toset(") || d.Attribute != "count" || d.Severity != model.SeverityInfo {
				t.Errorf("finding = %+v", d)
			}
		})
	}
}

func TestCountOverListMetadata(t *testing.T) {
	for _, r := range Rules() {
		if r.Code != "bp_count_over_list" {
			continue
		}
		if r.DefaultOff || r.Severity != model.SeverityInfo || r.Category != CategoryReliability {
			t.Errorf("rule = %+v", r)
		}
		if len(r.Pillars) != 2 || r.Pillars[0] != model.PillarReliability || r.Pillars[1] != model.PillarOperations {
			t.Errorf("pillars = %v", r.Pillars)
		}
		return
	}
	t.Fatal("bp_count_over_list is not a rule")
}
