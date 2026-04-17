variable "name_prefix" { type = string }
variable "project_id" { type = string }
variable "region" { type = string }
variable "network" { type = string }
variable "subnetwork" { type = string }
variable "pods_cidr" { type = string }
variable "services_cidr" { type = string }
variable "machine_type" { type = string }
variable "min_node_count" { type = number }
variable "max_node_count" { type = number }
variable "labels" { type = map(string); default = {} }
