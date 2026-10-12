resource "opalstack_app" "blog" {
  name      = "blog"
  osuser_id = opalstack_osuser.deploy.id
  type      = "APA"

  # Only the keys named here are managed.
  json = jsonencode({ php_version = 83 })
}
