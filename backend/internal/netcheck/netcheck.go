// Package netcheck analyzes the networks a workflow designs: a model of
// VPCs, subnets, route tables, gateways, network ACLs, security groups and
// the workloads placed in them (AWS; Azure and GCP get the address-range
// and rule checks that map cleanly), configuration checks reported as
// "net_*" diagnostics (each with why + fix), and a reachability analyzer
// in the spirit of AWS Reachability Analyzer.
//
// Values come from the sandbox's deployed state when every network
// resource of the design is deployed (exact CIDRs, IDs and count/for_each
// instances: basis "deployed"); otherwise the design's expressions are
// evaluated statically with variable defaults, the active environment's
// values, locals and count/for_each expansion (basis "design"). Checks
// whose inputs are unknown (module outputs, values computed at deploy
// time, unsupported functions) are skipped rather than guessed.
package netcheck

import (
	"sort"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/hclcodec"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Prefix starts every network finding's code.
const Prefix = "net_"

// IsFinding reports whether d is a network finding.
func IsFinding(d model.Diagnostic) bool { return strings.HasPrefix(d.Code, Prefix) }

// Strip returns diags without network findings.
func Strip(diags []model.Diagnostic) []model.Diagnostic {
	out := make([]model.Diagnostic, 0, len(diags))
	for _, d := range diags {
		if !IsFinding(d) {
			out = append(out, d)
		}
	}
	return out
}

// Analysis is the network model of one workflow.
type Analysis struct {
	wf          *model.Workflow
	basis       model.NetworkBasis
	basisReason string
	aws         *world
	others      *otherClouds
	findings    []model.Diagnostic
	notes       []string
}

func addrOf(n *model.Node) string { return hclcodec.Address(n) }

// networkTypes are the resource types the analysis reads; the deployed
// basis needs every one of them in the state.
func networkType(t string) bool {
	for _, p := range []string{"aws_vpc", "aws_default_vpc", "aws_subnet", "aws_default_subnet", "aws_route", "aws_main_route_table", "aws_default_route_table",
		"aws_internet_gateway", "aws_egress_only_internet_gateway", "aws_nat_gateway", "aws_eip", "aws_network_acl", "aws_default_network_acl",
		"aws_security_group", "aws_default_security_group", "aws_vpc_security_group", "aws_vpc_endpoint", "aws_vpc_peering_connection",
		"aws_ec2_transit_gateway_vpc_attachment", "aws_vpn_gateway", "aws_instance", "aws_network_interface", "aws_lambda_function",
		"aws_db_instance", "aws_db_subnet_group", "aws_rds_cluster", "aws_elasticache_", "aws_redshift_cluster", "aws_redshift_subnet_group",
		"aws_lb", "aws_alb", "aws_ecs_service", "aws_eks_", "aws_autoscaling_", "aws_launch_template", "aws_launch_configuration",
		"azurerm_virtual_network", "azurerm_subnet", "azurerm_network_security", "google_compute_network", "google_compute_subnetwork", "google_compute_firewall"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// Analyze builds the network model of wf (normalized). st is the
// sandbox's deployed state (nil or empty: design basis).
func Analyze(wf *model.Workflow, st *model.DeployedState) *Analysis {
	a := &Analysis{wf: wf, basis: model.NetworkBasisDesign}
	design, notes := evaluateDesign(wf)
	ents := design
	a.basisReason = "Nothing is deployed yet: values come from the design (variable defaults, the active environment and locals)."
	if st != nil && len(st.Resources) > 0 {
		deployed := deployedEntities(wf, st)
		inState := map[*model.Node]bool{}
		for _, e := range deployed {
			if e.node != nil {
				inState[e.node] = true
			}
		}
		var missing []string
		seen := map[*model.Node]bool{}
		for _, e := range design {
			if e.node != nil && networkType(e.typ) && !inState[e.node] && !seen[e.node] {
				seen[e.node] = true
				missing = append(missing, addrOf(e.node))
			}
		}
		if len(missing) == 0 {
			ents = deployed
			notes = nil
			a.basis = model.NetworkBasisDeployed
			a.basisReason = "Values come from the deployed sandbox (exact addresses, IDs and instances)."
			if !st.UpdatedAt.IsZero() {
				a.basisReason = "Values come from the sandbox deployed at " + st.UpdatedAt.UTC().Format("2006-01-02 15:04 UTC") + " (exact addresses, IDs and instances). Deploy again to analyze later design changes."
			}
		} else {
			sort.Strings(missing)
			more := ""
			if len(missing) > 3 {
				more = " and more"
				missing = missing[:3]
			}
			a.basisReason = "Some network resources aren't deployed yet (" + strings.Join(missing, ", ") + more + "): values come from the design."
		}
	}
	a.notes = append(a.notes, notes...)
	a.aws = buildAWS(a.basis, ents)
	a.others = buildOthers(a.aws)
	a.findings = append(a.aws.checks(), a.others.findings...)
	a.findings = dedupe(a.findings)
	if len(design) > 0 {
		for i := range wf.Nodes {
			if wf.Nodes[i].Kind == model.KindModule {
				a.notes = append(a.notes, "Module calls ("+addrOf(&wf.Nodes[i])+", ...) are not analyzed: their resources and outputs count as unknown.")
				break
			}
		}
	}
	multi := 0
	for _, e := range ents {
		if e.multi && networkType(e.typ) {
			multi++
		}
	}
	if multi > 0 {
		a.notes = append(a.notes, "Some count/for_each values are only known at deploy time; checks that depend on them were skipped.")
	}
	if a.aws.routesIncomplete || a.aws.assocIncomplete || a.aws.aclIncomplete || a.aws.sgIncomplete {
		a.notes = append(a.notes, "Some routes, associations or rules refer to resources Nimbo can't resolve (data sources, modules or IDs of existing resources); checks that depend on them were skipped.")
	}
	return a
}

func dedupe(diags []model.Diagnostic) []model.Diagnostic {
	type key struct{ code, msg, node, attr string }
	seen := map[key]bool{}
	out := []model.Diagnostic{}
	for _, d := range diags {
		k := key{d.Code, d.Message, d.NodeID, d.Attribute}
		if !seen[k] {
			seen[k] = true
			out = append(out, d)
		}
	}
	return out
}

// Check returns the network findings of wf's design (used on every save;
// no deployed state).
func Check(wf *model.Workflow) []model.Diagnostic { return Analyze(wf, nil).Findings() }

// Findings returns the net_* diagnostics.
func (a *Analysis) Findings() []model.Diagnostic { return a.findings }

// Basis says where the values came from.
func (a *Analysis) Basis() model.NetworkBasis { return a.basis }
