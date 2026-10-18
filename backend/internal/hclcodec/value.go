// Package hclcodec converts workflows to and from Terraform HCL, keeps the
// expression/literal forms of values in sync and derives graph edges from
// expressions.
package hclcodec

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	ctyjson "github.com/zclconf/go-cty/cty/json"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Address returns "aws_s3_bucket.b", "data.aws_iam_policy_document.p" or "module.vpc".
func Address(n *model.Node) string {
	switch n.Kind {
	case model.KindData:
		return "data." + n.Type + "." + n.Name
	case model.KindModule:
		return "module." + n.Name
	default:
		return n.Type + "." + n.Name
	}
}

// parseExpr parses a standalone expression. A trailing newline is appended
// because a heredoc must be terminated by one.
func parseExpr(expr string) (hclsyntax.Expression, error) {
	if strings.TrimSpace(expr) == "" {
		return nil, errors.New("expression is empty")
	}
	e, diags := hclsyntax.ParseExpression([]byte(expr+"\n"), "expression", hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diagsError(diags)
	}
	return e, nil
}

func diagsError(diags hcl.Diagnostics) error {
	var msgs []string
	for _, d := range diags {
		if d.Severity != hcl.DiagError {
			continue
		}
		msg := d.Summary
		if d.Detail != "" {
			msg += ": " + d.Detail
		}
		if d.Subject != nil {
			msg = fmt.Sprintf("line %d, column %d: %s", d.Subject.Start.Line, d.Subject.Start.Column, msg)
		}
		msgs = append(msgs, msg)
	}
	return errors.New(strings.Join(msgs, "; "))
}

// ValidateExpr reports a syntax error in a standalone expression.
func ValidateExpr(expr string) error {
	_, err := parseExpr(expr)
	return err
}

// ExprFromLiteral converts a JSON literal into HCL expression source.
func ExprFromLiteral(lit json.RawMessage) (string, error) {
	if len(lit) == 0 {
		return "", errors.New("literal is empty")
	}
	ty, err := ctyjson.ImpliedType(lit)
	if err != nil {
		return "", fmt.Errorf("invalid literal: %w", err)
	}
	v, err := ctyjson.Unmarshal(lit, ty)
	if err != nil {
		return "", fmt.Errorf("invalid literal: %w", err)
	}
	return string(hclwrite.Format(hclwrite.TokensForValue(v).Bytes())), nil
}

// LiteralFromExpr returns the JSON form of expr when it is a constant: no
// references, no function calls, no interpolated templates.
func LiteralFromExpr(expr string) (json.RawMessage, bool) {
	e, err := parseExpr(expr)
	if err != nil {
		return nil, false
	}
	return literalOf(e)
}

func literalOf(e hclsyntax.Expression) (json.RawMessage, bool) {
	if len(e.Variables()) > 0 || hasInterpolation(e) {
		return nil, false
	}
	v, diags := e.Value(nil)
	if diags.HasErrors() || !v.IsWhollyKnown() || v.IsMarked() {
		return nil, false
	}
	b, err := ctyjson.SimpleJSONValue{Value: v}.MarshalJSON()
	if err != nil {
		return nil, false
	}
	return b, true
}

// hasInterpolation reports whether e contains a template with anything other
// than literal text.
func hasInterpolation(e hclsyntax.Expression) bool {
	found := false
	hclsyntax.VisitAll(e, func(n hclsyntax.Node) hcl.Diagnostics {
		switch t := n.(type) {
		case *hclsyntax.TemplateWrapExpr, *hclsyntax.TemplateJoinExpr:
			found = true
		case *hclsyntax.TemplateExpr:
			for _, p := range t.Parts {
				if _, ok := p.(*hclsyntax.LiteralValueExpr); !ok {
					found = true
				}
			}
		}
		return nil
	})
	return found
}
