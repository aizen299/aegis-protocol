output "endpoint" { value = aws_db_instance.this.endpoint }
output "security_group_id" { value = aws_security_group.db.id }

# The master password is generated and rotated by AWS. Services read the DSN from SSM.
output "master_secret_arn" { value = aws_db_instance.this.master_user_secret[0].secret_arn }
