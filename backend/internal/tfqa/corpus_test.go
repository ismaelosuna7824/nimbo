//go:build corpus

package tfqa

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// corpusBuild is true when the tests are built with -tags corpus.
const corpusBuild = true

// The corpus: public Terraform repositories pinned to a commit. Each root
// is imported through the folder import with Scan set to the repository
// folder that contains the root and every local module it calls.
//
//	go test -tags corpus -timeout 3h ./internal/tfqa/ -run TestCorpus -v
//
// TFQA_CORPUS is the clone cache (default $TMPDIR/nimbo-tfqa-corpus),
// TFQA_PLUGIN_CACHE the shared provider cache, TFQA_REPORT a file that
// receives the results table and TFQA_VALIDATE=0 skips terraform
// init/validate.
type repo struct {
	Name, URL, Commit string
	Sparse            []string // sparse checkout paths; nil = whole repo
}

var repos = []repo{
	{Name: "terraform-aws-vpc", URL: "https://github.com/terraform-aws-modules/terraform-aws-vpc", Commit: "b3abd6df2ecf052451a361ed55b8f06f8742a795"},
	{Name: "terraform-aws-s3-bucket", URL: "https://github.com/terraform-aws-modules/terraform-aws-s3-bucket", Commit: "9afa4fc8c8f1e0b3fd913474c1bef8d0c454b235"},
	{Name: "terraform-aws-lambda", URL: "https://github.com/terraform-aws-modules/terraform-aws-lambda", Commit: "851d20939b25ee7ebadba96a3eab31ff6450134d"},
	{Name: "terraform-aws-sqs", URL: "https://github.com/terraform-aws-modules/terraform-aws-sqs", Commit: "a68b4d515f9ccc729fb8bf84c6760c6b91302ce0"},
	{Name: "terraform-aws-dynamodb-table", URL: "https://github.com/terraform-aws-modules/terraform-aws-dynamodb-table", Commit: "b6cc515760466a455ff0acb97b16151fdca4511e"},
	{Name: "terraform-aws-sns", URL: "https://github.com/terraform-aws-modules/terraform-aws-sns", Commit: "4538b7e208f5bbd6c9972f427f85c9bd735aedad"},
	{Name: "terraform-aws-eventbridge", URL: "https://github.com/terraform-aws-modules/terraform-aws-eventbridge", Commit: "f9934726324c988f823682884b4fa003586a7b6f"},
	{Name: "terraform-aws-apigateway-v2", URL: "https://github.com/terraform-aws-modules/terraform-aws-apigateway-v2", Commit: "95e6a1d56d6d761ec12c89b36b29aad03a421b2f"},
	{Name: "terraform-aws-rds", URL: "https://github.com/terraform-aws-modules/terraform-aws-rds", Commit: "175da043429cbbe41c512f976105719f6d0e538c"},
	{Name: "terraform-aws-ec2-instance", URL: "https://github.com/terraform-aws-modules/terraform-aws-ec2-instance", Commit: "4dfedcdd1f26006009c52439bbadba40a76ddb79"},
	{Name: "terraform-aws-iam", URL: "https://github.com/terraform-aws-modules/terraform-aws-iam", Commit: "da8e6a8673933ee651f0f0fe611d5ac6b9f9bcb9"},
	{Name: "terraform-aws-security-group", URL: "https://github.com/terraform-aws-modules/terraform-aws-security-group", Commit: "b3c1b2e8beff9671960f874f5569d77c803b0127"},
	{Name: "learn-terraform-lambda-api-gateway", URL: "https://github.com/hashicorp-education/learn-terraform-lambda-api-gateway", Commit: "9fa856d6015c1c2a24e9c5c8f4faf7101d47d039"},
	{Name: "learn-terraform-provision-eks-cluster", URL: "https://github.com/hashicorp-education/learn-terraform-provision-eks-cluster", Commit: "20816e4bcc757a242ba9be7544401a22b45b055e"},
	{Name: "learn-terraform-data-sources-vpc", URL: "https://github.com/hashicorp-education/learn-terraform-data-sources-vpc", Commit: "5204ab4c44aba124541a1a0224c3f2b84c3539d5"},
	{Name: "learn-terraform-data-sources-app", URL: "https://github.com/hashicorp-education/learn-terraform-data-sources-app", Commit: "bf30acbc5b5ec6819be830b00df2b7d7d133cdd1"},
	{Name: "learn-terraform-variables", URL: "https://github.com/hashicorp-education/learn-terraform-variables", Commit: "543b5e5d1332f0d7fb2c632dd6694ce5a8f998a9"},
	{Name: "learn-terraform-outputs", URL: "https://github.com/hashicorp-education/learn-terraform-outputs", Commit: "ef89e0b605b5c36475b794e17a864db87ae71bcd"},
	{Name: "learn-terraform-modules", URL: "https://github.com/hashicorp-education/learn-terraform-modules", Commit: "7d1154ec53aa778733403987b6a5c142ff7eb599"},
	{Name: "learn-terraform-provisioning", URL: "https://github.com/hashicorp-education/learn-terraform-provisioning", Commit: "d29c1e0a61e444d548401f274a888ba101ae8bd2"},
	{Name: "terraform-google-cloud-storage", URL: "https://github.com/terraform-google-modules/terraform-google-cloud-storage", Commit: "c7effc0be0f4de78ae45d8d9d426f8478cc397ca"},
	{Name: "terraform-google-pubsub", URL: "https://github.com/terraform-google-modules/terraform-google-pubsub", Commit: "6cf1860af9c1ccd7228f0ac5e75b4c6755f75d17"},
	{Name: "terraform-google-network", URL: "https://github.com/terraform-google-modules/terraform-google-network", Commit: "91352bed3014ddbfb323d979b6413a90e7253acf"},
	{Name: "terraform-provider-azurerm", URL: "https://github.com/hashicorp/terraform-provider-azurerm", Commit: "bb0454fbdfeadd1ae7d1c67c6eb35c58b4995e9b", Sparse: []string{"examples"}},
	{Name: "terraform-provider-oci", URL: "https://github.com/oracle/terraform-provider-oci", Commit: "3b4efe9832138b0e198ec6c8925fe26c83ee7ba2", Sparse: []string{"examples/object_storage", "examples/functions", "examples/networking"}},
}

// corpusRoot is a root inside a repo: Scan relative to the repo, Project
// relative to Scan.
type corpusRoot struct{ Repo, Scan, Project string }

var corpusRoots = []corpusRoot{
	{"terraform-aws-vpc", ".", "examples/simple"},
	{"terraform-aws-vpc", ".", "examples/complete"},
	{"terraform-aws-vpc", ".", "examples/ipv6-dualstack"},
	{"terraform-aws-s3-bucket", ".", "examples/complete"},
	{"terraform-aws-s3-bucket", ".", "examples/object"},
	{"terraform-aws-s3-bucket", ".", "examples/notification"},
	{"terraform-aws-lambda", ".", "examples/simple"},
	{"terraform-aws-lambda", ".", "examples/complete"},
	{"terraform-aws-lambda", ".", "examples/triggers"},
	{"terraform-aws-lambda", ".", "examples/alias"},
	{"terraform-aws-sqs", ".", "examples/complete"},
	{"terraform-aws-dynamodb-table", ".", "examples/basic"},
	{"terraform-aws-dynamodb-table", ".", "examples/autoscaling"},
	{"terraform-aws-dynamodb-table", ".", "examples/s3-import"},
	{"terraform-aws-sns", ".", "examples/complete"},
	{"terraform-aws-eventbridge", ".", "examples/complete"},
	{"terraform-aws-eventbridge", ".", "examples/with-schedules"},
	{"terraform-aws-apigateway-v2", ".", "examples/complete-http"},
	{"terraform-aws-apigateway-v2", ".", "examples/websocket"},
	{"terraform-aws-rds", ".", "examples/complete-postgres"},
	{"terraform-aws-rds", ".", "examples/s3-import-mysql"},
	{"terraform-aws-ec2-instance", ".", "examples/complete"},
	{"terraform-aws-iam", ".", "examples/iam-role"},
	{"terraform-aws-iam", ".", "examples/iam-user"},
	{"terraform-aws-security-group", ".", "examples/complete"},
	{"learn-terraform-lambda-api-gateway", ".", "."},
	{"learn-terraform-provision-eks-cluster", ".", "."},
	{"learn-terraform-data-sources-vpc", ".", "."},
	{"learn-terraform-data-sources-app", ".", "."},
	{"learn-terraform-variables", ".", "."},
	{"learn-terraform-outputs", ".", "."},
	{"learn-terraform-modules", ".", "."},
	{"learn-terraform-provisioning", ".", "instances"},
	{"terraform-google-cloud-storage", ".", "examples/simple_bucket"},
	{"terraform-google-cloud-storage", ".", "examples/multiple_buckets"},
	{"terraform-google-pubsub", ".", "examples/simple"},
	{"terraform-google-pubsub", ".", "examples/cloud_storage"},
	{"terraform-google-network", ".", "examples/basic_custom_mode"},
	{"terraform-google-network", ".", "examples/submodule_firewall"},
	{"terraform-google-network", ".", "examples/simple_project"},
	{"terraform-provider-azurerm", "examples/storage", "storage-account"},
	{"terraform-provider-azurerm", "examples/servicebus", "basic"},
	{"terraform-provider-azurerm", "examples/virtual-networks", "basic"},
	{"terraform-provider-azurerm", "examples/app-service", "linux-web-app-zip-deploy"},
	{"terraform-provider-azurerm", "examples/container-registry", "."},
	{"terraform-provider-oci", "examples/object_storage", "."},
	{"terraform-provider-oci", "examples/functions", "."},
	{"terraform-provider-oci", "examples/networking", "vcn"},
}

func corpusDir() string {
	if d := os.Getenv("TFQA_CORPUS"); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "nimbo-tfqa-corpus")
}

// fetch makes dir a checkout of r at its pinned commit.
func fetch(t *testing.T, r repo, dir string) {
	t.Helper()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if head, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output(); err == nil && strings.TrimSpace(string(head)) == r.Commit {
		return
	}
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("remote", "add", "origin", r.URL)
	if r.Sparse != nil {
		git("sparse-checkout", "set", "--no-cone")
		git(append([]string{"sparse-checkout", "set"}, r.Sparse...)...)
		git("fetch", "-q", "--depth", "1", "--filter=blob:none", "origin", r.Commit)
	} else {
		git("fetch", "-q", "--depth", "1", "origin", r.Commit)
	}
	git("checkout", "-q", "FETCH_HEAD")
}

func TestCorpus(t *testing.T) {
	base := corpusDir()
	byName := map[string]repo{}
	for _, r := range repos {
		byName[r.Name] = r
		fetch(t, r, filepath.Join(base, r.Name))
	}
	opts := localOptions()
	var reps []*report
	for _, cr := range corpusRoots {
		name := cr.Repo + "/" + strings.TrimPrefix(filepath.ToSlash(filepath.Join(cr.Scan, cr.Project)), "./")
		r := root{Name: name, Scan: filepath.Join(base, cr.Repo, filepath.FromSlash(cr.Scan)), Project: cr.Project}
		t.Run(name, func(t *testing.T) {
			rep, _, _ := checkRoot(t, r, opts)
			reps = append(reps, rep)
		})
	}
	table := formatTable(reps)
	t.Log("\n" + table)
	if p := os.Getenv("TFQA_REPORT"); p != "" {
		if err := os.WriteFile(p, []byte(table), 0o644); err != nil {
			t.Error(err)
		}
	}
}
