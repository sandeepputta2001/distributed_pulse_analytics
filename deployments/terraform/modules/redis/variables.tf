variable "name_prefix" { type = string }
variable "region" { type = string }
variable "memory_size_gb" { type = number }
variable "tier" { type = string }
variable "network" { type = string }
variable "labels" { type = map(string); default = {} }
