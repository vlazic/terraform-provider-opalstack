resource "opalstack_psqldb" "shop" {
  name      = "shop"
  server_id = data.opalstack_server.web.id
  user_ids  = [opalstack_psqluser.shop.id]
}
