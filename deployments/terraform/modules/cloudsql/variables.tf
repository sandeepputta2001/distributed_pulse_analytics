variable "name_prefix" { type = string }
variable "region" { type = string }
variable "zone" { type = string }
variable "tier" { type = string }
variable "postgres_version" { type = string }
variable "postgres_password" { type = string; sensitive = true }
variable "network" { type = string }
variable "labels" { type = map(string); default = {} }
