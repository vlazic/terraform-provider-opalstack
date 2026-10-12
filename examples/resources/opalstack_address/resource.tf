resource "opalstack_address" "info" {
  source       = "info@${opalstack_domain.example.name}"
  mailuser_ids = [opalstack_mailuser.info.id]
  forwards     = ["someone@example.org"]
}
