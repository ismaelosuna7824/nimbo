// Package compliance maps security benchmarks (CIS Foundations per cloud, CIS Docker, CIS Kubernetes, later SOC 2 /
// HIPAA) onto the best-practice rules Nimbo already runs on a design, and grades a workflow against them. It is a
// pre-audit based on the design, never a certification: controls outside the design (account settings, processes)
// are manual and only the user can attest them.
package compliance

import (
	"slices"
	"strings"
	"sync"

	"github.com/ismaelosuna7824/nimbo/backend/internal/lint"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Framework is one benchmark for one platform.
type Framework struct {
	// ID is stable and versioned, e.g. "cis-aws-3.0".
	ID      string `json:"id"`
	Name    string `json:"name"`    // "CIS AWS Foundations Benchmark"
	Version string `json:"version"` // "3.0.0"
	// Platform the framework applies to: "aws" | "azurerm" | "google" | "oci" (Terraform clouds), "docker", "kubernetes".
	Platform string `json:"platform"`
	// Source is the official page of the benchmark.
	Source   string    `json:"source"`
	Controls []Control `json:"controls"`
}

// Control is one recommendation of a framework.
type Control struct {
	// ID as the benchmark numbers it, e.g. "2.1.4".
	ID      string `json:"id"`
	Title   string `json:"title"`
	Section string `json:"section"` // "Storage", "Networking", …
	// Rules are the best-practice rule codes (bp_*, dk_*, k8s_*) whose findings decide the control. Empty for manual
	// controls.
	Rules []string `json:"rules,omitempty"`
	// AppliesTo lists the resource/object types the control is about (e.g. "aws_s3_bucket", "service" for compose,
	// "Deployment" for Kubernetes). A workflow with none of them gets "not applicable". Empty = always applies.
	AppliesTo []string `json:"appliesTo,omitempty"`
	// AppliesToNode narrows AppliesTo for Terraform designs: when set, only resources of an AppliesTo type for which it
	// returns true are applicable (a SQL Server flag control on SQL Server instances only); none → not applicable. It
	// should return true when it can't tell (a value that is an expression). Docker and Kubernetes resources ignore it.
	AppliesToNode func(n *model.Node) bool `json:"-"`
	// Counts narrows Rules for a rule that decides several controls (dk_privileged flags privileged, host networking
	// and host PID): when set, only findings for which it returns true count for this control. It should key on a
	// stable field the rule sets (Diagnostic.Attribute), never on the message.
	Counts func(d model.Diagnostic) bool `json:"-"`
	// Manual controls can't be seen in a design (account settings, MFA, processes): the user attests them.
	Manual bool `json:"manual,omitempty"`
	// Guidance says, in plain words, what to do (manual) or how Nimbo checks it (automatic).
	Guidance string `json:"guidance"`
}

var (
	mu         sync.RWMutex
	frameworks []Framework
)

// Register adds a framework; provider files call it from init(). IDs must be unique.
func Register(f Framework) {
	mu.Lock()
	defer mu.Unlock()
	if slices.ContainsFunc(frameworks, func(x Framework) bool { return x.ID == f.ID }) {
		panic("compliance: duplicate framework " + f.ID)
	}
	frameworks = append(frameworks, f)
}

// Frameworks returns every registered framework, ordered by platform then ID.
func Frameworks() []Framework {
	mu.RLock()
	defer mu.RUnlock()
	out := slices.Clone(frameworks)
	slices.SortFunc(out, func(a, b Framework) int {
		if a.Platform != b.Platform {
			if a.Platform < b.Platform {
				return -1
			}
			return 1
		}
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return out
}

// onAttribute is a Control.Counts keeping the findings about one of fields: the finding's Attribute is the field
// (compose: "privileged") or a field path ending in it (Kubernetes: "spec.template.spec.hostPID").
func onAttribute(fields ...string) func(d model.Diagnostic) bool {
	return func(d model.Diagnostic) bool {
		for _, f := range fields {
			if d.Attribute == f || strings.HasSuffix(d.Attribute, "."+f) {
				return true
			}
		}
		return false
	}
}

// exposesPort is a Control.Counts keeping the bp_open_ingress findings about port (lint.ExposesPort): the rule
// covers SSH, RDP and database ports together, a benchmark control usually one of them.
func exposesPort(port int) func(d model.Diagnostic) bool {
	return func(d model.Diagnostic) bool { return lint.ExposesPort(d, port) }
}
