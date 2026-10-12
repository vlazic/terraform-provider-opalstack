data "opalstack_ip" "v4" {
  server_id = data.opalstack_server.web.id
}

resource "opalstack_site" "blog" {
  name       = "blog"
  domain_ids = [opalstack_domain.www.id]
  ip4_id     = data.opalstack_ip.v4.id

  routes = [
    { app_id = opalstack_app.blog.id },
    { app_id = opalstack_app.static.id, uri = "/assets/" },
  ]

  generate_le = true
}
