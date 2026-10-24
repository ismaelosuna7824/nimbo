package catalog

import (
	"fmt"
	"slices"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/access"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Bucket policy recipes: the S3 resource-policy patterns that need a
// Condition, which the AWS policy editor shows read-only.
//   - s3-require-tls: the "deny insecure transport" statement of the S3
//     security best practices (aws:SecureTransport = false).
//   - cloudfront-s3-oac: CloudFront origin access control, as in "Restrict
//     access to an Amazon S3 origin" (OAC + origin + a bucket policy
//     statement for cloudfront.amazonaws.com conditioned on AWS:SourceArn).
func s3PolicyRecipes() []recipe {
	isBucket := isType("aws_s3_bucket")
	return []recipe{
		{
			id: "s3-require-tls", label: "Require HTTPS for the bucket (deny insecure transport)",
			desc:  "Attaches the policy to the bucket and adds a Deny statement for every principal and s3:* on the bucket and its objects when aws:SecureTransport is false.",
			match: pair(isType("aws_s3_bucket_policy"), isBucket), bidi: true,
			sets: []string{"src.bucket"},
			apply: func(x *conn) error {
				pol, b := x.src, x.dst
				setAttr(&pol.Body, "bucket", expr(ref(b, "id")))
				if pab := x.publicAccessBlock(b); pab != nil && !dependsOn(pol, pab) {
					dependOn(pol, pab)
				}
				return x.addBucketStatement(pol, "aws:SecureTransport", model.AWSStatement{
					Sid: "DenyInsecureTransport", Effect: "Deny",
					Principals: []model.AWSPrincipal{{Type: "*", Values: []string{`"*"`}}},
					Actions:    []string{"s3:*"},
					Resources:  []string{ref(b, "arn"), fmt.Sprintf(`"${%s}/*"`, ref(b, "arn"))},
				}, "Bool", "aws:SecureTransport", `"false"`)
			},
		},
		{
			id: "cloudfront-s3-oac", label: "Serve the bucket through CloudFront (OAC)",
			desc:  "Adds an origin access control (S3, always sign, SigV4), an origin for the bucket's REST endpoint with it, and a bucket policy statement letting only this distribution (cloudfront.amazonaws.com with AWS:SourceArn) read objects.",
			match: pair(isType("aws_cloudfront_distribution"), isBucket), bidi: true,
			apply: func(x *conn) error {
				dist, b := x.src, x.dst
				pol := x.bucketPolicy(b)
				if pol == nil {
					pol = x.helper(model.KindResource, "aws_s3_bucket_policy", b.Name+"_policy")
					setAttr(&pol.Body, "bucket", expr(ref(b, "id")))
					if pab := x.publicAccessBlock(b); pab != nil {
						dependOn(pol, pab)
					}
				}
				oac := x.helper(model.KindResource, "aws_cloudfront_origin_access_control", b.Name)
				setAttr(&oac.Body, "name", expr(ref(b, "id")))
				setAttr(&oac.Body, "origin_access_control_origin_type", lit("s3"))
				setAttr(&oac.Body, "signing_behavior", lit("always"))
				setAttr(&oac.Body, "signing_protocol", lit("sigv4"))
				originID := "s3-" + strings.ReplaceAll(b.Name, "_", "-")
				// The bucket's origin, else an origin the palette left blank.
				var origin *model.Block
				for i, blk := range dist.Body.Blocks {
					if blk.Type != "origin" {
						continue
					}
					if a := getAttr(&blk.Body, "domain_name"); a != nil && referencesNode(valueSource(a.Value), address(b)) {
						origin = &dist.Body.Blocks[i]
						break
					}
					if origin == nil && blankAttr(&blk.Body, "domain_name") {
						origin = &dist.Body.Blocks[i]
					}
				}
				if origin == nil {
					dist.Body.Blocks = append(dist.Body.Blocks, model.Block{Type: "origin", Body: emptyBody()})
					origin = &dist.Body.Blocks[len(dist.Body.Blocks)-1]
				}
				if blankAttr(&origin.Body, "origin_id") {
					setAttr(&origin.Body, "origin_id", lit(originID))
				}
				setAttr(&origin.Body, "domain_name", expr(ref(b, "bucket_regional_domain_name")))
				setAttr(&origin.Body, "origin_access_control_id", expr(ref(oac, "id")))
				id := getAttr(&origin.Body, "origin_id").Value
				for i := range dist.Body.Blocks {
					if blk := &dist.Body.Blocks[i]; blk.Type == "default_cache_behavior" && blankAttr(&blk.Body, "target_origin_id") {
						setAttr(&blk.Body, "target_origin_id", id)
					}
				}
				return x.addBucketStatement(pol, "cloudfront.amazonaws.com", model.AWSStatement{
					Sid: "AllowCloudFrontServicePrincipalReadOnly", Effect: "Allow",
					Principals: []model.AWSPrincipal{{Type: "Service", Values: []string{`"cloudfront.amazonaws.com"`}}},
					Actions:    []string{"s3:GetObject"},
					Resources:  []string{fmt.Sprintf(`"${%s}/*"`, ref(b, "arn"))},
				}, "StringEquals", "AWS:SourceArn", ref(dist, "arn"))
			},
		},
	}
}

// blankAttr: name is unset or an empty string (a palette placeholder).
func blankAttr(b *model.Body, name string) bool {
	a := getAttr(b, name)
	return a == nil || strings.TrimSpace(valueSource(a.Value)) == `""`
}

// bucketPolicy is the aws_s3_bucket_policy attached to bucket, if any (a
// bucket has one policy).
func (x *conn) bucketPolicy(bucket *model.Node) *model.Node {
	return x.attachedTo(bucket, "aws_s3_bucket_policy")
}

func (x *conn) publicAccessBlock(bucket *model.Node) *model.Node {
	return x.attachedTo(bucket, "aws_s3_bucket_public_access_block")
}

func (x *conn) attachedTo(bucket *model.Node, typ string) *model.Node {
	for _, n := range x.b.all() {
		if n.Kind == model.KindResource && n.Type == typ && access.AWSRefersTo(n, "bucket", bucket) {
			return n
		}
	}
	return nil
}

// addBucketStatement appends st with Condition = { test = { key = value } }
// (value an HCL expression) to the bucket policy's document, unless a
// statement already mentions marker (the pattern is in place).
func (x *conn) addBucketStatement(pol *model.Node, marker string, st model.AWSStatement, test, key, value string) error {
	all := x.b.all()
	doc, ok := access.AWSDocument(all, pol, "policy")
	if !ok || !doc.Editable {
		return access.Editf("catalog: the policy of %s can't be edited here; add the statement in code", address(pol))
	}
	if slices.ContainsFunc(doc.Statements, func(s model.AWSStatement) bool {
		return slices.ContainsFunc(s.Extra, func(e model.AccessExtra) bool { return strings.Contains(e.Source, marker) }) ||
			slices.ContainsFunc(s.Principals, func(p model.AWSPrincipal) bool { return slices.ContainsFunc(p.Values, func(v string) bool { return strings.Contains(v, marker) }) })
	}) {
		return nil
	}
	var cond string
	switch doc.Encoding {
	case "jsonencode":
		cond = fmt.Sprintf("{ %s = { %q = %s } }", test, key, value)
	case "json":
		v := value
		if !strings.HasPrefix(v, `"`) {
			v = `"${` + v + `}"`
		}
		cond = fmt.Sprintf(`{"%s": {"%s": %s}}`, test, key, v)
	default:
		return access.Editf("catalog: conditions can only be added to a jsonencode or JSON policy; %s's policy is %s", address(pol), doc.Encoding)
	}
	st.Sid = awsSid(doc.Statements, st.Sid)
	st.Editable = true
	st.Extra = []model.AccessExtra{{Key: "Condition", Source: cond}}
	doc.Statements = append(doc.Statements, st)
	return access.AWSWrite(all, pol, doc)
}
