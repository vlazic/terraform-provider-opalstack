data "opalstack_psqldb" "wiki" {
  name = "wiki"
}

output "wiki_users" {
  value = data.opalstack_psqldb.wiki.user_ids
}
