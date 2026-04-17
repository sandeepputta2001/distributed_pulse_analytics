# Terraform — PulseAnalytics GCP/GKE Infrastructure

Provisions the complete GCP infrastructure for PulseAnalytics across three environments (dev / staging / prod).

## Resources Created

| Resource | Description |
|----------|-------------|
| VPC + Subnet | Private VPC, secondary ranges for GKE pods/services, Cloud NAT, Private Services Access |
| GKE Cluster | Regional cluster (HA), private nodes, Workload Identity, auto-scaling node pool |
| Cloud SQL (Postgres 16) | Private IP, automated backups, Query Insights |
| Memorystore (Redis 7) | AUTH enabled, RDB persistence, HA in staging/prod |
| Artifact Registry | Docker registry for service images |
| Secret Manager | JWT secret, Postgres password |
| IAM / Workload Identity | Per-service GCP SAs bound to K8s SAs |

## Directory Structure

```
terraform/
├── main.tf              # Root module — wires all modules together
├── variables.tf         # Input variable definitions
├── outputs.tf           # Exported values (cluster endpoint, DB IP, etc.)
├── versions.tf          # Provider version constraints
├── modules/
│   ├── vpc/             # VPC, subnets, NAT, PSA
│   ├── gke/             # GKE cluster + node pools
│   ├── cloudsql/        # Cloud SQL Postgres
│   ├── redis/           # Memorystore Redis
│   └── iam/             # Service accounts + Workload Identity
└── environments/
    ├── dev/             # Dev-specific sizing + backend config
    ├── staging/
    └── prod/
```

## Prerequisites

1. GCP project with billing enabled.
2. `gcloud` CLI authenticated: `gcloud auth application-default login`
3. GCS bucket for Terraform state: `gsutil mb gs://pulse-analytics-tfstate`
4. Terraform ≥ 1.7: `brew install terraform`

## Usage

```bash
cd environments/dev

# Copy and fill in your values
cp terraform.tfvars.example terraform.tfvars
vim terraform.tfvars

# Initialize (downloads providers, configures backend)
terraform init

# Preview changes
terraform plan

# Apply
terraform apply

# Get kubectl config
$(terraform output -raw kubectl_config_command)
```

## Updating an Environment

```bash
cd environments/staging
terraform plan -var image_tag=v1.2.3
terraform apply -var image_tag=v1.2.3
```

## Environment Sizing

| Setting | dev | staging | prod |
|---------|-----|---------|------|
| Node type | e2-standard-2 | e2-standard-4 | e2-standard-8 |
| Nodes | 1–3 | 1–5 | 3–20 |
| Postgres tier | db-g1-small | db-custom-2-7680 | db-custom-4-15360 |
| Redis GB | 1 (BASIC) | 2 (HA) | 8 (HA) |

## Secrets

Never commit `terraform.tfvars`. All sensitive values (`postgres_password`, `jwt_secret`) are also stored in GCP Secret Manager and accessed by pods via Workload Identity.

## Destroying

```bash
# Dev only — prod has deletion_protection enabled
terraform destroy
```
