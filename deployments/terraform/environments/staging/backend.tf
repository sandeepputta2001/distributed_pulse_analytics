terraform {
  backend "gcs" {
    bucket = "pulse-analytics-tfstate"
    prefix = "staging"
  }
}
