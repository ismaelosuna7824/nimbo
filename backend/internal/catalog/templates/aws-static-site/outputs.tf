output "bucket" {
  value       = aws_s3_bucket.site.bucket
  description = "Name of the bucket that hosts the site."
}

output "website_endpoint" {
  value       = aws_s3_bucket_website_configuration.site.website_endpoint
  description = "Website endpoint of the bucket (open it in a browser)."
}
