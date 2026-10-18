# Terraform merges this into resource "aws_security_group" "web".
resource "aws_security_group" "web" {
  revoke_rules_on_delete = true
}
