package compose

import "strings"

// Runtime facts of popular images that the hardening editor needs: the
// unprivileged user an image documents (for user:), and the variables its
// entrypoint also reads from a file named by <VAR>_FILE (for secrets
// mounted under /run/secrets). Only what each image's official
// documentation states is listed.

type imageRuntime struct {
	user    string
	fileEnv []string
	source  string // where it is documented
}

var (
	postgresRuntime = imageRuntime{
		user: "postgres",
		// "Docker Secrets ... Currently, this is only supported for
		// POSTGRES_INITDB_ARGS, POSTGRES_PASSWORD, POSTGRES_USER, and
		// POSTGRES_DB"; "Arbitrary --user Notes": the image runs as any
		// user owning the data directory (a new named volume is owned by
		// postgres).
		fileEnv: []string{"POSTGRES_PASSWORD", "POSTGRES_USER", "POSTGRES_DB", "POSTGRES_INITDB_ARGS"},
		source:  "https://hub.docker.com/_/postgres",
	}
	// The :nonroot tags run as the nonroot user (uid 65532).
	distrolessRuntime = imageRuntime{user: "nonroot", source: "https://github.com/GoogleContainerTools/distroless"}
)

// imageRuntimes is keyed by image repository (ParseImage(...).Repo).
var imageRuntimes = map[string]imageRuntime{
	// "The node images provide the node user with uid 1000 for such purpose".
	"node":                  {user: "node", source: "https://github.com/nodejs/docker-node/blob/main/docs/BestPractices.md#non-root-user"},
	"postgres":              postgresRuntime,
	"postgis/postgis":       postgresRuntime, // built on the postgres image's entrypoint
	"timescale/timescaledb": postgresRuntime,
	"mysql": {
		// "it is possible to invoke this image with --user set to any value
		// (other than root/0)"; "_FILE ... only supported for ...".
		user:    "mysql",
		fileEnv: []string{"MYSQL_ROOT_PASSWORD", "MYSQL_ROOT_HOST", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD"},
		source:  "https://hub.docker.com/_/mysql",
	},
	"mariadb": {
		fileEnv: []string{"MARIADB_ROOT_PASSWORD", "MARIADB_ROOT_PASSWORD_HASH", "MARIADB_ROOT_HOST", "MARIADB_DATABASE", "MARIADB_USER", "MARIADB_PASSWORD", "MARIADB_PASSWORD_HASH",
			"MYSQL_ROOT_PASSWORD", "MYSQL_ROOT_HOST", "MYSQL_DATABASE", "MYSQL_USER", "MYSQL_PASSWORD"},
		source: "https://mariadb.com/kb/en/mariadb-server-docker-official-image-environment-variables/",
	},
	"mongo": {
		fileEnv: []string{"MONGO_INITDB_ROOT_USERNAME", "MONGO_INITDB_ROOT_PASSWORD"},
		source:  "https://hub.docker.com/_/mongo",
	},
}

// runtimeOf returns the runtime facts of an image reference.
func runtimeOf(ref string) (imageRuntime, bool) {
	img := ParseImage(ref)
	if img == nil {
		return imageRuntime{}, false
	}
	if strings.HasPrefix(img.Repo, "gcr.io/distroless/") && strings.Contains(img.Tag, "nonroot") {
		return distrolessRuntime, true
	}
	r, ok := imageRuntimes[strings.TrimPrefix(strings.TrimPrefix(img.Repo, "docker.io/"), "library/")]
	return r, ok
}
