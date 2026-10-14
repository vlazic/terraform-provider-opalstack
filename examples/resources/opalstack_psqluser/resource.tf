data "opalstack_server" "web" {
  hostname = "opal1.opalstack.com"
}

# Without a password the API chooses one. With pgp_key the state keeps it only
# encrypted to that key, in encrypted_default_password; otherwise it is in
# default_password.
resource "opalstack_psqluser" "shop" {
  name      = "shop"
  server_id = data.opalstack_server.web.id
  pgp_key   = file("${path.module}/ops.asc")
}
