output "execution_role_arn" { value = aws_iam_role.execution.arn }
output "api_task_role_arn" { value = aws_iam_role.api_task.arn }
output "indexer_task_role_arn" { value = aws_iam_role.indexer_task.arn }
