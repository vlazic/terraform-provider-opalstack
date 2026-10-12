data "opalstack_server" "web" {
  hostname = "opal1.opalstack.com"
}

resource "opalstack_osuser" "deploy" {
  name      = "deploy"
  server_id = data.opalstack_server.web.id
}

# The API chooses the password and reports it once.
output "deploy_password" {
  value     = opalstack_osuser.deploy.default_password
  sensitive = true
}
