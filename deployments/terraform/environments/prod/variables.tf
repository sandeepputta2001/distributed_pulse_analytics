variable "project_id" { type = string }
variable "postgres_password" { type = string; sensitive = true }
variable "jwt_secret" { type = string; sensitive = true }
variable "image_tag" { type = string }
