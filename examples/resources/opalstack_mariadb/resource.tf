resource "opalstack_mariadb" "blog" {
  name      = "blog"
  server_id = data.opalstack_server.web.id
  charset   = "utf8mb4"
  user_ids  = [opalstack_mariauser.blog.id]
}
