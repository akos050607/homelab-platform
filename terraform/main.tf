# terraform/main.tf
terraform {
  required_providers {
    hcloud = { source = "hetznercloud/hcloud", version = "~> 1.48" }
  }
}

resource "hcloud_ssh_key" "me" {
  name       = "akos"
  public_key = file("~/.ssh/id_ed25519.pub")
}

resource "hcloud_firewall" "k3s" {
  name = "k3s-public"
  dynamic "rule" {
    for_each = ["22", "80", "443"]
    content {
      direction  = "in"
      protocol   = "tcp"
      port       = rule.value
      source_ips = ["0.0.0.0/0", "::/0"]
    }
  }
}

resource "hcloud_server" "cloud" {
  name         = "k3s-server"
  image        = "ubuntu-24.04"
  server_type  = "cx33"
  location     = "nbg1"
  ssh_keys     = [hcloud_ssh_key.me.id]
  firewall_ids = [hcloud_firewall.k3s.id]
}

output "public_ip" { value = hcloud_server.cloud.ipv4_address }