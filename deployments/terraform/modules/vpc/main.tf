resource "google_compute_network" "pulse" {
  name                    = "${var.name_prefix}-vpc"
  auto_create_subnetworks = false
  routing_mode            = "REGIONAL"
}

resource "google_compute_subnetwork" "pulse" {
  name          = "${var.name_prefix}-subnet"
  region        = var.region
  network       = google_compute_network.pulse.id
  ip_cidr_range = "10.0.0.0/20"

  secondary_ip_range {
    range_name    = "${var.name_prefix}-pods"
    ip_cidr_range = "10.16.0.0/16"
  }

  secondary_ip_range {
    range_name    = "${var.name_prefix}-services"
    ip_cidr_range = "10.32.0.0/20"
  }

  private_ip_google_access = true
}

# Cloud NAT — outbound internet for nodes without public IPs
resource "google_compute_router" "pulse" {
  name    = "${var.name_prefix}-router"
  region  = var.region
  network = google_compute_network.pulse.id
}

resource "google_compute_router_nat" "pulse" {
  name                               = "${var.name_prefix}-nat"
  router                             = google_compute_router.pulse.name
  region                             = var.region
  nat_ip_allocate_option             = "AUTO_ONLY"
  source_subnetwork_ip_ranges_to_nat = "ALL_SUBNETWORKS_ALL_IP_RANGES"
}

# Private Services Access — required for Cloud SQL and Memorystore
resource "google_compute_global_address" "private_service_range" {
  name          = "${var.name_prefix}-psa-range"
  purpose       = "VPC_PEERING"
  address_type  = "INTERNAL"
  prefix_length = 16
  network       = google_compute_network.pulse.id
}

resource "google_service_networking_connection" "private_service" {
  network                 = google_compute_network.pulse.id
  service                 = "servicenetworking.googleapis.com"
  reserved_peering_ranges = [google_compute_global_address.private_service_range.name]
}

# Basic firewall rules
resource "google_compute_firewall" "internal" {
  name    = "${var.name_prefix}-allow-internal"
  network = google_compute_network.pulse.name

  allow {
    protocol = "tcp"
  }
  allow {
    protocol = "udp"
  }
  allow {
    protocol = "icmp"
  }

  source_ranges = ["10.0.0.0/8"]
}
