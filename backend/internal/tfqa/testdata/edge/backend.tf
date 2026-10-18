# A second terraform block: Terraform merges every terraform block.
terraform {
  backend "local" {
    path = "state/terraform.tfstate"
  }
}
