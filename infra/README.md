# Infrastructure

Terraform. Every AWS resource is defined here — no console-driven infrastructure.

## Status

| Environment | State |
|---|---|
| `environments/staging` | Defined, not applied |
| `environments/production` | Not created — Phase 1 is staging only |

## Commands

```bash
terraform fmt -check -recursive
terraform init -backend=false && terraform validate   # from infra/, no credentials needed

cd environments/staging
terraform init
terraform plan -out=tfplan
terraform apply tfplan
```

## Layout

The root module composes; environments configure. An environment directory sets the S3 backend and
passes variables — it declares no resources of its own, so staging and production cannot drift
structurally.

```
infra/
  main.tf              root composition
  modules/
    vpc/               subnets, NAT per AZ, ALB and service security groups
    iam/               execution role + per-service task roles
    rds/               PostgreSQL, private, SG-gated
    elasticache/       Redis, failover pair
    s3/                artifact bucket
    ecs/               cluster, ECR, task definitions, services, ALB, alarms
  environments/staging/
```

## Decisions worth knowing

**NAT gateway per AZ.** A single shared NAT is a cross-AZ dependency that takes the whole VPC's
egress down when its AZ degrades.

**No `*` in resource policies.** The one wildcard is `kms:Decrypt`, constrained by a `kms:ViaService`
condition to SSM — the key ID is not known until the parameter is created.

**Task roles are per-service.** The indexer and API have different blast radii, so they never share
a role. Only the execution role is shared, because it grants no application permissions.

**The indexer is a singleton.** `desired_count = 1` with `maximum_percent = 100`, so a deploy stops
the old task before starting the new one. Two indexers on one chain would contend for the same
cursor row.

**Secrets never enter Terraform.** The RDS master password is generated and rotated by AWS
(`manage_master_user_password`). Services read connection strings from SSM SecureString parameters
injected at task start. `*.tfvars` is gitignored.

**Immutable ECR tags.** CI pushes the commit SHA; `latest` is only the local default.
