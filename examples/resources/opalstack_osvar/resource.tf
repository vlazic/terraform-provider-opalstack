data "opalstack_osuser" "web" {
  name = "example_user"
}

resource "opalstack_osvar" "node_env" {
  name       = "NODE_ENV"
  content    = "production"
  osuser_ids = [data.opalstack_osuser.web.id]
}
