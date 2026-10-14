data "opalstack_server" "web" {
  hostname = "opal1.opalstack.com"
}

variable "blog_db_password" {
  type      = string
  sensitive = true
  ephemeral = true
}

# The password is sent and never kept in the state. Raise the version to send a new one.
resource "opalstack_mariauser" "blog" {
  name                = "blog"
  server_id           = data.opalstack_server.web.id
  password_wo         = var.blog_db_password
  password_wo_version = 1
}
