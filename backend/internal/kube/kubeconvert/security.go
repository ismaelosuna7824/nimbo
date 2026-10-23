package kubeconvert

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ismaelosuna7824/nimbo/backend/internal/compose"
	"github.com/ismaelosuna7824/nimbo/backend/internal/model"
)

// Kubernetes can only enforce runAsNonRoot for numeric users, so a named
// USER needs its UID: from the Dockerfile (useradd -u), the base image's
// well-known users, or the official images that run fine as their own user.

type imageUser struct {
	uid, gid int64
}

// knownUsers are users of popular base images by name.
var knownUsers = map[string]imageUser{
	"nonroot":  {65532, 65532}, // distroless, chainguard
	"nobody":   {65534, 65534},
	"node":     {1000, 1000}, // node images
	"www-data": {33, 33},
	"daemon":   {1, 1},
}

// baseUsers are users a base image family defines ("app" of the .NET 8+
// images).
var baseUsers = map[string]map[string]imageUser{
	"mcr.microsoft.com/dotnet": {"app": {1654, 1654}},
}

// nonRootImages are official images whose entrypoint works when started as
// their own user (with the data directory writable through fsGroup).
func nonRootImage(ref *model.ImageRef) (imageUser, bool) {
	if ref == nil {
		return imageUser{}, false
	}
	repo := strings.TrimPrefix(strings.TrimPrefix(ref.Repo, "docker.io/"), "library/")
	alpine := strings.Contains(ref.Tag, "alpine")
	switch repo {
	case "postgres", "postgis/postgis", "pgvector/pgvector":
		if alpine {
			return imageUser{70, 70}, true
		}
		return imageUser{999, 999}, true
	case "redis", "valkey/valkey":
		if alpine {
			return imageUser{999, 1000}, true
		}
		return imageUser{999, 999}, true
	case "mysql", "mariadb", "mongo":
		return imageUser{999, 999}, true
	case "rabbitmq":
		if alpine {
			return imageUser{100, 101}, true
		}
		return imageUser{999, 999}, true
	case "memcached":
		return imageUser{11211, 11211}, true
	}
	if strings.HasPrefix(repo, "bitnami/") {
		return imageUser{1001, 1001}, true
	}
	if strings.HasPrefix(repo, "gcr.io/distroless/") && strings.Contains(ref.Tag, "nonroot") {
		return imageUser{65532, 65532}, true
	}
	return imageUser{}, false
}

var (
	userAddRe = regexp.MustCompile(`\b(useradd|adduser)\b([^;&|]*)`)
	uidFlagRe = regexp.MustCompile(`(?:-u|--uid)[ =](\d+)`)
)

// parseUser splits "name[:group]" and reports numeric parts.
func parseUser(s string) (name string, uid, gid int64, numeric bool) {
	name, group, _ := strings.Cut(strings.TrimSpace(s), ":")
	if n, err := strconv.ParseInt(name, 10, 64); err == nil {
		uid, numeric = n, true
		gid = n
		if g, err := strconv.ParseInt(group, 10, 64); err == nil {
			gid = g
		}
	}
	return name, uid, gid, numeric
}

// dockerfileUID finds the UID the Dockerfile gives user name: an explicit
// useradd/adduser -u, or the first free UID (1000) of a user created on a
// base without regular users.
func (c *converter) dockerfileUID(u *unit, name string) (int64, bool) {
	if u.facts.Build == nil {
		return 0, false
	}
	df := c.p.Dockerfiles[u.facts.Build.Dockerfile]
	if df == nil || len(df.Stages) == 0 {
		return 0, false
	}
	// The final stage and the stages it builds on.
	target := len(df.Stages) - 1
	if t := strings.ToLower(u.svc.Build.Target); t != "" {
		for i, st := range df.Stages {
			if st.Name == t {
				target = i
			}
		}
	}
	chain := []int{target}
	for {
		base := strings.ToLower(df.Stages[chain[len(chain)-1]].Base)
		next := -1
		for i, st := range df.Stages[:chain[len(chain)-1]] {
			if st.Name != "" && st.Name == base {
				next = i
			}
		}
		if next < 0 {
			break
		}
		chain = append(chain, next)
	}
	created := false
	for _, si := range chain {
		for _, ii := range df.Stages[si].Instructions {
			in := df.Instructions[ii]
			if in.Cmd != "RUN" {
				continue
			}
			for _, m := range userAddRe.FindAllStringSubmatch(in.Args, -1) {
				fields := strings.Fields(m[2])
				if len(fields) == 0 || fields[len(fields)-1] != name {
					continue
				}
				if um := uidFlagRe.FindStringSubmatch(m[2]); um != nil {
					n, _ := strconv.ParseInt(um[1], 10, 64)
					return n, true
				}
				created = true
			}
		}
	}
	if !created {
		return 0, false
	}
	// useradd/adduser pick the first free UID from 1000; node images
	// already have node=1000 and recent ubuntu ubuntu=1000.
	base := ""
	if n := len(u.facts.BaseImages); n > 0 {
		base = u.facts.BaseImages[n-1]
	}
	ref := compose.ParseImage(base)
	if ref != nil && (strings.HasSuffix(ref.Repo, "node") || strings.HasSuffix(ref.Repo, "ubuntu") && ref.Tag >= "23") {
		return 1001, true
	}
	return 1000, true
}

func (c *converter) security(u *unit) {
	s := u.svc
	u.sec.capAdd, u.sec.capDrop, u.sec.readOnly = s.CapAdd, s.CapDrop, s.ReadOnly
	user := s.User
	from := "user"
	if user == "" {
		user, from = u.facts.User, "Dockerfile USER"
	}
	name, uid, gid, numeric := parseUser(user)
	switch {
	case user == "" && u.build == nil:
		if iu, ok := nonRootImage(compose.ParseImage(u.image)); ok {
			u.sec.uid, u.sec.gid, u.sec.nonRoot = iu.uid, iu.gid, true
			c.mapped(u.name, "image "+u.image, fmt.Sprintf("runAsNonRoot as the image's own user (UID %d, fsGroup %d): its entrypoint supports it", iu.uid, iu.gid))
		} else {
			c.issue(u, "info", "user", fmt.Sprintf("%s runs as root (its entrypoint needs it, or its user is unknown): runAsNonRoot isn't set; privilege escalation is still blocked.", u.image))
		}
	case user == "" || name == "root" || numeric && uid == 0:
		c.issue(u, "info", "user", "The container runs as root: add a USER to the Dockerfile (with a numeric UID) to run it as non-root.")
	case numeric:
		u.sec.uid, u.sec.gid, u.sec.nonRoot = uid, gid, true
		c.mapped(u.name, from+": "+user, fmt.Sprintf("runAsNonRoot, runAsUser %d", uid))
	default:
		iu, ok := knownUsers[name]
		for prefix, users := range baseUsers {
			for _, b := range u.facts.BaseImages {
				if strings.HasPrefix(b, prefix) {
					if bu, found := users[name]; found {
						iu, ok = bu, true
					}
				}
			}
		}
		if n, found := c.dockerfileUID(u, name); found {
			iu, ok = imageUser{n, n}, true
		}
		if !ok {
			c.issue(u, "info", "user", fmt.Sprintf("%s %s has no known numeric UID, so runAsNonRoot can't be enforced: write USER with a UID (e.g. USER 10001).", from, user))
			return
		}
		u.sec.uid, u.sec.gid, u.sec.nonRoot = iu.uid, iu.gid, true
		c.mapped(u.name, from+": "+user, fmt.Sprintf("runAsNonRoot, runAsUser %d (UID of %s)", iu.uid, name))
	}
	if u.sec.nonRoot && (len(u.claims) > 0 || len(u.pvcs) > 0 || hasPVC(u)) {
		u.sec.fsGroup = true
	}
}

func hasPVC(u *unit) bool {
	for _, m := range u.mounts {
		if m.source.kind == "persistentVolumeClaim" || m.source.kind == "claimTemplate" {
			return true
		}
	}
	return false
}
