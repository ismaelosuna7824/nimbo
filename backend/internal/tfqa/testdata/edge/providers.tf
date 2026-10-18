provider "aws" {
  region = var.region

  default_tags {
    tags = {
      Project = "nimbo-edge"
      Owner   = "Café ☕ team" # unicode survives
    }
  }
}

// Second region for replicas.
provider "aws" {
  alias  = "west"
  region = "us-west-2"
}
