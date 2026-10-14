data "opalstack_server" "web" {
  hostname = "opal1.opalstack.com"
}

# The API chooses the password and reports it once. With pgp_key the state keeps
# it only encrypted: decrypt it with
#   terraform output -raw deploy_password_encrypted | base64 -d | gpg --decrypt
resource "opalstack_osuser" "deploy" {
  name      = "deploy"
  server_id = data.opalstack_server.web.id
  pgp_key   = file("${path.module}/ops.asc")
}

output "deploy_password_encrypted" {
  value = opalstack_osuser.deploy.encrypted_default_password
}

# A password of your own, kept out of the state. Raise the version to send a new one.
variable "files_password" {
  type      = string
  sensitive = true
  ephemeral = true
}

resource "opalstack_osuser" "files" {
  name                = "files"
  server_id           = data.opalstack_server.web.id
  password_wo         = var.files_password
  password_wo_version = 1
}
