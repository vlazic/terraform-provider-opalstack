terraform {
  required_providers {
    opalstack = {
      source = "vlazic/opalstack"
    }
  }
}

# The token is read from the OPALSTACK_TOKEN environment variable.
provider "opalstack" {}
