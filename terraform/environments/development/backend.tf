terraform {
  backend "gcs" {
    bucket = "apex-bkt-tf-state"
    prefix = "terraform/state/apex-probe-service/development"
  }
}
