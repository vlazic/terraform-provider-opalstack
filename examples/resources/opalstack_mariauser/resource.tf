data "opalstack_server" "web" {
  hostname = "opal1.opalstack.com"
}

resource "opalstack_mariauser" "blog" {
  name      = "blog"
  server_id = data.opalstack_server.web.id
  password  = var.blog_db_password
}
