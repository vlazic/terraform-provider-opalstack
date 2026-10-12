data "opalstack_app" "blog" {
  name = "blog"
}

output "blog_settings" {
  value = jsondecode(data.opalstack_app.blog.json)
}
