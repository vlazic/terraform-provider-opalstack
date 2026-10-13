data "opalstack_address" "info" {
  source = "info@example.com"
}

output "info_mailboxes" {
  value = data.opalstack_address.info.mailuser_ids
}
