package lint

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

const directions = `(?:north|south|east|west|central|northeast|southeast|northwest|southwest)`

var (
	awsRegion  = regexp.MustCompile(`^(?:us|eu|ap|sa|ca|me|af|il|mx)-(?:gov-|iso-|isob-)?` + directions + `-\d$`)
	awsAZ      = regexp.MustCompile(`^(?:us|eu|ap|sa|ca|me|af|il|mx)-(?:gov-|iso-|isob-)?` + directions + `-\d[a-z]$`)
	gcpRegion  = regexp.MustCompile(`^(?:us|europe|asia|australia|northamerica|southamerica|me|africa)-` + directions + `\d+$`)
	gcpZone    = regexp.MustCompile(`^(?:us|europe|asia|australia|northamerica|southamerica|me|africa)-` + directions + `\d+-[a-f]$`)
	arnAccount = regexp.MustCompile(`arn:aws[a-z-]*:[a-z0-9-]+:[a-z0-9-]*:(\d{12}):`)
	accountID  = regexp.MustCompile(`^\d{12}$`)
)

// literalStrings walks the constant strings of every resource attribute.
func (p *pass) literalStrings(fn func(n *model.Node, path string, s string)) {
	for _, n := range p.resources() {
		walkAttrs(&n.Body, "", func(path string, a *model.Attr) {
			if path == "provider" || path == "depends_on" {
				return
			}
			for _, s := range stringsOf(a.Value) {
				fn(n, path, s)
			}
		})
	}
}

func lastSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 {
		return path[i+1:]
	}
	return path
}

func checkHardcodedRegion(p *pass) {
	seen := map[string]bool{}
	p.literalStrings(func(n *model.Node, path, s string) {
		cloud, _ := p.cat.CloudOf(n.Type)
		var fix string
		switch {
		case awsRegion.MatchString(s) && cloud == model.CloudAWS:
			fix = "Use var.aws_region or data.aws_region.current.region (the provider's region) instead of the literal."
		case gcpRegion.MatchString(s) && cloud == model.CloudGCP:
			fix = "Use var.gcp_region (the region the provider block also uses) instead of the literal."
		case cloud == model.CloudAzure && lastSegment(path) == "location" && s != "":
			fix = "Use var.location, or the resource group's location (azurerm_resource_group.<name>.location)."
		default:
			return
		}
		key := n.ID + "\x00" + path
		if seen[key] {
			return
		}
		seen[key] = true
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s hardcodes the region %q in %s", addr(n), s, path),
			why:     "A hardcoded region pins the resource to one place: deploying to another region or environment needs code edits instead of a variable.",
			fix:     fix,
		})
	})
}

func checkHardcodedAZ(p *pass) {
	p.literalStrings(func(n *model.Node, path, s string) {
		if !awsAZ.MatchString(s) && !gcpZone.MatchString(s) {
			return
		}
		fix := "Use data.aws_availability_zones.available.names[<index>] or a variable."
		if gcpZone.MatchString(s) {
			fix = "Use a variable (e.g. var.gcp_zone) or data.google_compute_zones.available.names[<index>]."
		}
		p.report(finding{
			node: n, attribute: path,
			message: fmt.Sprintf("%s hardcodes the availability zone %q in %s", addr(n), s, path),
			why:     "Zone names differ per region (and per account for AWS), so a literal zone breaks the code in another region.",
			fix:     fix,
		})
	})
}

func checkHardcodedAccount(p *pass) {
	for _, n := range p.resources() {
		walkAttrs(&n.Body, "", func(path string, a *model.Attr) {
			id := ""
			if m := arnAccount.FindStringSubmatch(source(a.Value)); m != nil {
				id = m[1]
			} else {
				seg := strings.ToLower(lastSegment(path))
				if strings.Contains(seg, "account") || strings.Contains(seg, "owner") {
					for _, s := range stringsOf(a.Value) {
						if accountID.MatchString(s) {
							id = s
						}
					}
				}
			}
			if id == "" {
				return
			}
			p.report(finding{
				node: n, attribute: path,
				message: fmt.Sprintf("%s hardcodes the AWS account ID %s in %s", addr(n), id, path),
				why:     "Account IDs change between environments (dev, staging, prod accounts); hardcoded ones make the code single-account and leak account details.",
				fix:     "Use data.aws_caller_identity.current.account_id (and data.aws_partition.current.partition for ARNs), or reference the resource's arn attribute.",
			})
		})
	}
}
