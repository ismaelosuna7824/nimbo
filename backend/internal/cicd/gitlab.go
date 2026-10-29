package cicd

import (
	"slices"

	"go.yaml.in/yaml/v4"
)

// Images of the GitLab jobs.
const (
	imageTerraform  = "hashicorp/terraform"
	imageKube       = "alpine/k8s:1.36.5" // kubectl, helm, kubeconform, curl
	imageDocker     = "docker:29"
	imageDockerDind = "docker:29-dind"
	imageDockerCLI  = "docker:29-cli"
	imageAlpine     = "alpine:3.22"
)

const (
	glReview    = `$CI_PIPELINE_SOURCE == "merge_request_event"`
	glRelease   = `$CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH`
	glDuplicate = `$CI_COMMIT_BRANCH && $CI_OPEN_MERGE_REQUESTS`
)

// gcpCredentials writes the external account credentials of workload
// identity federation, reading the job's GitLab ID token.
const gcpCredentials = `printf '%s' "$GCP_ID_TOKEN" > /tmp/gcp-oidc-token
cat > /tmp/gcp-credentials.json <<EOF
{"type": "external_account", "audience": "//iam.googleapis.com/$GCP_WORKLOAD_IDENTITY_PROVIDER", "subject_token_type": "urn:ietf:params:oauth:token-type:jwt", "token_url": "https://sts.googleapis.com/v1/token", "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/$GCP_SERVICE_ACCOUNT:generateAccessToken", "credential_source": {"file": "/tmp/gcp-oidc-token"}}
EOF
export GOOGLE_APPLICATION_CREDENTIALS=/tmp/gcp-credentials.json`

func (g *gen) gitlabDoc() *yaml.Node {
	var used []string
	for _, s := range stages {
		if slices.ContainsFunc(g.jobs, func(j job) bool { return j.stage == s }) {
			used = append(used, s)
		}
	}
	// Merge request pipelines for merge requests, branch pipelines
	// otherwise, never both for one push.
	doc := mapping(
		"stages", strSeq(used...),
		"workflow", mapping("rules", seq(
			mapping("if", glReview),
			mapping("if", glDuplicate, "when", "never"),
			mapping("if", "$CI_COMMIT_BRANCH"),
		)),
	)
	if len(g.globals) > 0 {
		vars := mapping()
		for _, kv := range g.globals {
			set(vars, kv[0], kv[1])
		}
		set(doc, "variables", vars)
	}
	for _, j := range g.jobs {
		set(doc, j.id, g.gitlabJob(j))
	}
	return doc
}

func (g *gen) gitlabJob(j job) *yaml.Node {
	m := mapping("stage", j.stage)
	vars := mapping()
	var before []string
	switch {
	case j.uses(toolTerraform):
		tag := g.tfVersion
		if tag == "" {
			tag = "latest"
		}
		set(m, "image", mapping("name", imageTerraform+":"+tag, "entrypoint", strSeq("")))
	case j.uses(toolKubectl):
		set(m, "image", imageKube)
	case j.uses(toolDockerBuild):
		set(m, "image", imageDocker)
		set(m, "services", strSeq(imageDockerDind))
		set(vars, "DOCKER_TLS_CERTDIR", "/certs")
	case j.uses(toolCompose):
		set(m, "image", imageDockerCLI)
	default:
		set(m, "image", imageAlpine)
	}
	if j.uses(toolTerraform) && j.uses(toolDockerBuild) {
		// Service builds from the Terraform image: Alpine's docker CLI and
		// Buildx against the dind service.
		set(m, "services", strSeq(imageDockerDind))
		set(vars, "DOCKER_HOST", "tcp://docker:2376")
		set(vars, "DOCKER_TLS_CERTDIR", "/certs")
		set(vars, "DOCKER_TLS_VERIFY", "1")
		set(vars, "DOCKER_CERT_PATH", "/certs/client")
		before = append(before, "apk add --no-cache docker-cli docker-cli-buildx")
	}
	ids := mapping()
	for _, t := range j.tools {
		switch t {
		case toolMake:
			before = append(before, "apk add --no-cache make")
		case toolSSH:
			before = append(before, "apk add --no-cache openssh-client git")
		case toolCurl:
			before = append(before, "apk add --no-cache curl")
		case toolRegistryPush:
			before = append(before, `printf '%s' "$CI_REGISTRY_PASSWORD" | docker login "$CI_REGISTRY" -u "$CI_REGISTRY_USER" --password-stdin`)
		case toolRegistryPull:
			set(vars, "REGISTRY", "$CI_REGISTRY")
			set(vars, "REGISTRY_USER", "$CI_REGISTRY_USER")
			set(vars, "REGISTRY_PASSWORD", "$CI_REGISTRY_PASSWORD")
		case toolAWS:
			set(ids, "AWS_ID_TOKEN", mapping("aud", "sts.amazonaws.com"))
			before = append(before, "printf '%s' \"$AWS_ID_TOKEN\" > /tmp/aws-web-identity-token\nexport AWS_WEB_IDENTITY_TOKEN_FILE=/tmp/aws-web-identity-token")
		case toolAzure:
			set(ids, "ARM_OIDC_TOKEN", mapping("aud", "api://AzureADTokenExchange"))
			before = append(before, `export ARM_USE_OIDC=true ARM_CLIENT_ID="$AZURE_CLIENT_ID" ARM_TENANT_ID="$AZURE_TENANT_ID" ARM_SUBSCRIPTION_ID="$AZURE_SUBSCRIPTION_ID"`)
		case toolGCP:
			set(ids, "GCP_ID_TOKEN", mapping("aud", "https://iam.googleapis.com/$GCP_WORKLOAD_IDENTITY_PROVIDER"))
			before = append(before, gcpCredentials)
		}
	}
	if len(vars.Content) > 0 {
		set(m, "variables", vars)
	}
	if len(ids.Content) > 0 {
		set(m, "id_tokens", ids)
	}
	set(m, "needs", j.needs)
	switch j.on {
	case review:
		set(m, "rules", seq(mapping("if", glReview)))
	case release:
		rule := mapping("if", glRelease)
		if j.gated {
			set(rule, "when", "manual")
		}
		set(m, "rules", seq(rule))
		if j.gated {
			// A manual job holds the pipeline (and the next environments)
			// until it runs, instead of being skipped as optional.
			set(m, "allow_failure", false)
		}
	}
	switch {
	case j.deploy:
		set(m, "environment", mapping("name", j.env))
	case j.verify:
		set(m, "environment", mapping("name", j.env, "action", "verify"))
	case j.env != "":
		set(m, "environment", mapping("name", j.env, "action", "prepare"))
	}
	if j.lock != "" {
		set(m, "resource_group", j.lock)
	}
	if len(before) > 0 {
		set(m, "before_script", strSeq(before...))
	}
	script := make([]string, len(j.steps))
	for i, s := range j.steps {
		script[i] = s.run
	}
	set(m, "script", strSeq(script...))
	if len(j.artifacts) > 0 {
		a := mapping("name", j.artifactName, "paths", j.artifacts)
		if j.stage == stagePlan {
			set(a, "expose_as", j.name)
		}
		set(m, "artifacts", a)
	}
	return m
}
