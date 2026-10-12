data "opalstack_server" "web" {
  hostname = "opal1.opalstack.com"
}

# Without a password the API chooses one and reports it in default_password.
resource "opalstack_psqluser" "shop" {
  name      = "shop"
  server_id = data.opalstack_server.web.id
}
