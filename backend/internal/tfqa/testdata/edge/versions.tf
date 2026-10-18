/*
 * Edge-case root module for Nimbo's Terraform fidelity tests.
 * Every construct here must survive import -> export unchanged.
 */

# Toolchain pins.
terraform {
  required_version = ">= 1.10.0" # ephemeral variables

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.40" # provider-defined functions
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
    archive = {
      source  = "hashicorp/archive"
      version = "~> 2.4"
    }
  }
  // the backend is configured per environment
}
