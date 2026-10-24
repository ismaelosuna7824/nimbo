package model

import "time"

// ---------------------------------------------------------------------------
// Learn: per-provider roadmaps (beginner → expert) and the challenges they
// link to. Content comes from the content migrations embedded in the daemon
// (backend/internal/learn/migrations) and lives in <home>/learn.db with the
// user's progress. The challenge content type (Challenge) is in
// challenge.go.

// Learn providers: the clouds, the local runtimes and "global"
// (provider-agnostic challenges with per-provider adaptations).
const (
	LearnProviderAWS        = "aws"
	LearnProviderAzure      = "azure"
	LearnProviderGCP        = "gcp"
	LearnProviderOCI        = "oci"
	LearnProviderDocker     = "docker"
	LearnProviderKubernetes = "kubernetes"
	LearnProviderGlobal     = "global"
)

// LearnProviders is every provider in display order.
var LearnProviders = []string{
	LearnProviderAWS, LearnProviderAzure, LearnProviderGCP, LearnProviderOCI,
	LearnProviderDocker, LearnProviderKubernetes, LearnProviderGlobal,
}

// Learn levels.
const (
	LearnLevelBeginner     = "beginner"
	LearnLevelIntermediate = "intermediate"
	LearnLevelAdvanced     = "advanced"
	LearnLevelExpert       = "expert"
)

// LearnLevels is every level from easiest to hardest.
var LearnLevels = []string{LearnLevelBeginner, LearnLevelIntermediate, LearnLevelAdvanced, LearnLevelExpert}

// Challenge runtimes: local = Docker or k3s, emulated = Floci (AWS),
// design = validate/plan-level only.
const (
	LearnRuntimeLocal    = "local"
	LearnRuntimeEmulated = "emulated"
	LearnRuntimeDesign   = "design"
)

// LearnRuntimes is every challenge runtime.
var LearnRuntimes = []string{LearnRuntimeLocal, LearnRuntimeEmulated, LearnRuntimeDesign}

// Roadmap is a provider's learning path. ID is "<provider>-roadmap"; a
// provider has at most one.
type Roadmap struct {
	ID       string         `json:"id"`
	Provider string         `json:"provider"`
	Title    string         `json:"title"`
	Summary  string         `json:"summary"`
	Levels   []RoadmapLevel `json:"levels"`
	Sources  []LearnSource  `json:"sources"`
}

// RoadmapLevel is one stage of a roadmap; levels are listed easiest first.
type RoadmapLevel struct {
	Level string `json:"level"`
	Title string `json:"title"`
	// Goals is markdown: what the learner can do after this level.
	Goals string        `json:"goals"`
	Steps []RoadmapStep `json:"steps"`
}

// RoadmapStep is a topic with its practice challenges and official docs.
type RoadmapStep struct {
	Title       string        `json:"title"`
	Description string        `json:"description"` // markdown
	Challenges  []string      `json:"challenges,omitempty"`
	Docs        []LearnSource `json:"docs"`
}

// Challenge progress states (ChallengeProgress.Status).
const (
	LearnStatusNotStarted = "not_started"
	LearnStatusInProgress = "in_progress"
	LearnStatusCompleted  = "completed"
)

// ChallengeProgress is the user's progress on a challenge. Migrations
// never change it: it survives content updates and removals.
type ChallengeProgress struct {
	ChallengeID string `json:"challengeId"`
	Status      string `json:"status"`
	// Attempts counts finished "Check my solution" runs.
	Attempts int `json:"attempts"`
	// CompletedGoals are the check IDs that passed at least once.
	CompletedGoals []string `json:"completedGoals"`
	// WorkflowID is the workflow of the latest start or check.
	WorkflowID  string     `json:"workflowId,omitempty"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	UpdatedAt   *time.Time `json:"updatedAt,omitempty"`
	CompletedAt *time.Time `json:"completedAt,omitempty"`
}

// ChallengeSummary is a challenge in a list, with the user's progress.
type ChallengeSummary struct {
	ID            string            `json:"id"`
	Provider      string            `json:"provider"`
	Level         string            `json:"level"`
	Title         string            `json:"title"`
	Summary       string            `json:"summary"`
	Minutes       int               `json:"minutes"`
	Tags          []string          `json:"tags"`
	Runtime       string            `json:"runtime"`
	Prerequisites []string          `json:"prerequisites"`
	Checks        int               `json:"checks"`
	Progress      ChallengeProgress `json:"progress"`
}

// ChallengeFilter narrows GET /learn/challenges; empty fields match all.
// Search matches the title, summary, problem and tags (case-insensitive).
type ChallengeFilter struct {
	Provider string
	Level    string
	Tag      string
	Search   string
}

// LearnPrerequisite is a prerequisite of a challenge with its title and
// whether the user completed it.
type LearnPrerequisite struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Completed bool   `json:"completed"`
	// Missing: the prerequisite is not in the learn database.
	Missing bool `json:"missing,omitempty"`
}

// ChallengeDetail is GET /learn/challenges/{id}.
type ChallengeDetail struct {
	Challenge     Challenge           `json:"challenge"`
	Progress      ChallengeProgress   `json:"progress"`
	Prerequisites []LearnPrerequisite `json:"prerequisites"`
	// Roadmap steps that link to this challenge ("<level>: <step title>";
	// other providers' roadmaps prefixed "<Provider> roadmap · ").
	Steps []string `json:"steps"`
}

// LearnLevelCount counts a provider's challenges at one level.
type LearnLevelCount struct {
	Level      string `json:"level"`
	Challenges int    `json:"challenges"`
	Completed  int    `json:"completed"`
}

// LearnProvider is one tab of the Learn page.
type LearnProvider struct {
	ID         string            `json:"id"`
	Title      string            `json:"title"`
	HasRoadmap bool              `json:"hasRoadmap"`
	Challenges int               `json:"challenges"`
	Completed  int               `json:"completed"`
	Levels     []LearnLevelCount `json:"levels"`
}

// LearnStatus reports the content migrations: how many are applied and
// the load errors (a migration that failed to apply, an applied
// migration that was edited, broken references). Errors do not stop the
// daemon; the Learn page shows them.
type LearnStatus struct {
	Applied int      `json:"applied"`
	Pending int      `json:"pending"`
	Errors  []string `json:"errors"`
}

// LearnOverview is GET /learn: the providers and the content status.
type LearnOverview struct {
	Providers []LearnProvider `json:"providers"`
	Status    LearnStatus     `json:"status"`
}

// LearnProgressUpdate is PUT /learn/progress/{id}: "not_started" resets
// the challenge's progress, "in_progress" marks it started. Completion is
// only recorded by a passing check.
type LearnProgressUpdate struct {
	Status string `json:"status"`
}
