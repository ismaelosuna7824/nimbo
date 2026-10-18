variable "compartment_id" {
  type = string
}

resource "aws_s3_bucket" "east" {
  bucket = "logs-east"
}

resource "aws_s3_bucket" "west" {
  provider = aws.west
  bucket   = "logs-west"
}

resource "aws_s3_bucket_replication_configuration" "east_to_west" {
  role   = "arn:aws:iam::000000000000:role/replication"
  bucket = aws_s3_bucket.east.id

  rule {
    id     = "all"
    status = "Enabled"

    destination {
      bucket = aws_s3_bucket.west.arn
    }
  }
}

resource "google_storage_bucket" "archive" {
  name     = "archive-${aws_s3_bucket.east.bucket}"
  location = "US"
}

resource "oci_objectstorage_bucket" "backup" {
  compartment_id = var.compartment_id
  name           = "backup"
  namespace      = "demo"
}
