# Virtual bank accounts: CloudFront -> S3 (SPA) + Lambda Function URL (API) -> EFS (SQLite). See aws-architecture.drawio.

terraform {
  required_version = ">= 1.9"
  required_providers {
    aws     = { source = "hashicorp/aws", version = "~> 5.0" }
    archive = { source = "hashicorp/archive", version = "~> 2.4" }
    random  = { source = "hashicorp/random", version = "~> 3.6" }
  }
  backend "s3" {
    bucket         = "vbank-tfstate"
    key            = "prod/terraform.tfstate"
    region         = "ca-central-1"
    encrypt        = true
    use_lockfile   = true # Native S3 locking (Requires Terraform v1.9.0+)
  }
}

# CloudFront sends this header to the Function URL; the API rejects requests without it (api.Handler).
resource "random_password" "origin_secret" {
  length  = 40
  special = false
}

variable "region" {
  type    = string
  default = "ca-central-1"
}

variable "name" {
  type    = string
  default = "vbank"
}

provider "aws" {
  region = var.region
}

data "aws_availability_zones" "az" { state = "available" }

# ---------- Network: private subnets only, no NAT (Lambda needs no internet) ----------

resource "aws_vpc" "main" {
  cidr_block           = "10.0.0.0/24"
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags                 = { Name = var.name }
}

resource "aws_subnet" "private" {
  count             = 2
  vpc_id            = aws_vpc.main.id
  cidr_block        = cidrsubnet(aws_vpc.main.cidr_block, 2, count.index)
  availability_zone = data.aws_availability_zones.az.names[count.index]
  tags              = { Name = "${var.name}-private-${count.index}" }
}

resource "aws_security_group" "lambda" {
  name   = "${var.name}-lambda"
  vpc_id = aws_vpc.main.id
  egress {
    from_port   = 2049
    to_port     = 2049
    protocol    = "tcp"
    cidr_blocks = [aws_vpc.main.cidr_block]
  }
}

resource "aws_security_group" "efs" {
  name   = "${var.name}-efs"
  vpc_id = aws_vpc.main.id
  ingress {
    from_port       = 2049
    to_port         = 2049
    protocol        = "tcp"
    security_groups = [aws_security_group.lambda.id]
  }
}

# ---------- EFS (holds bank.db) + daily backups ----------

resource "aws_efs_file_system" "db" {
  encrypted = true
  tags      = { Name = "${var.name}-db" }
  lifecycle { prevent_destroy = true } # holds the only copy of the data
}

resource "aws_efs_mount_target" "db" {
  count           = length(aws_subnet.private)
  file_system_id  = aws_efs_file_system.db.id
  subnet_id       = aws_subnet.private[count.index].id
  security_groups = [aws_security_group.efs.id]
}

resource "aws_efs_access_point" "db" {
  file_system_id = aws_efs_file_system.db.id
  posix_user {
    uid = 1000
    gid = 1000
  }
  root_directory {
    path = "/db"
    creation_info {
      owner_uid   = 1000
      owner_gid   = 1000
      permissions = "750"
    }
  }
}

# Turns on the AWS Backup default plan for this file system: daily, 35-day retention.
resource "aws_efs_backup_policy" "db" {
  file_system_id = aws_efs_file_system.db.id
  backup_policy { status = "ENABLED" }
}

# ---------- Lambda (API) ----------

# Builds the Go binary (bootstrap) and copies db/*.sql into .build/pkg.
data "archive_file" "lambda" {
  type             = "zip"
  source_dir       = "${path.module}/.build/pkg"
  output_path      = "${path.module}/.build/lambda.zip"
  output_file_mode = "0755" # bootstrap must be executable; Windows files carry no exec bit
}

resource "aws_iam_role" "lambda" {
  name = "${var.name}-lambda"
  assume_role_policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = "sts:AssumeRole", Principal = { Service = "lambda.amazonaws.com" } }]
  })
}

resource "aws_iam_role_policy_attachment" "lambda" {
  for_each = toset([
    "arn:aws:iam::aws:policy/service-role/AWSLambdaVPCAccessExecutionRole", # ENIs + CloudWatch Logs
    "arn:aws:iam::aws:policy/AmazonElasticFileSystemClientReadWriteAccess",
  ])
  role       = aws_iam_role.lambda.name
  policy_arn = each.value
}

resource "aws_cloudwatch_log_group" "lambda" {
  name              = "/aws/lambda/${var.name}-api"
  retention_in_days = 14
}

resource "aws_lambda_function" "api" {
  function_name    = "${var.name}-api"
  role             = aws_iam_role.lambda.arn
  runtime          = "provided.al2023"
  handler          = "bootstrap"
  architectures    = ["arm64"] # Graviton: ~20% cheaper per GB-second
  filename         = data.archive_file.lambda.output_path
  source_code_hash = data.archive_file.lambda.output_base64sha256
  memory_size      = 512 # bcrypt is CPU-bound; Lambda CPU scales with memory
  timeout          = 10
  # One writer at a time: SQLite locking over EFS (NFS) is the weakest part of this design, so don't allow two.
  #reserved_concurrent_executions = 1

  vpc_config {
    subnet_ids         = aws_subnet.private[*].id
    security_group_ids = [aws_security_group.lambda.id]
  }

  file_system_config {
    arn              = aws_efs_access_point.db.arn
    local_mount_path = "/mnt/db"
  }

  environment {
    variables = { DB_PATH = "/mnt/db/bank.db", ORIGIN_SECRET = random_password.origin_secret.result }
  }

  depends_on = [aws_efs_mount_target.db, aws_iam_role_policy_attachment.lambda, aws_cloudwatch_log_group.lambda]
}

# Daily auto-funding: fires hourly at :59 UTC; the Lambda funds the families whose local time is then 23:xx.
resource "aws_iam_role" "scheduler" {
  name = "${var.name}-scheduler"
  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "scheduler.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })
}

resource "aws_iam_role_policy" "scheduler" {
  role = aws_iam_role.scheduler.id
  policy = jsonencode({
    Version   = "2012-10-17"
    Statement = [{ Effect = "Allow", Action = "lambda:InvokeFunction", Resource = aws_lambda_function.api.arn }]
  })
}

resource "aws_scheduler_schedule" "daily_funding" {
  name                         = "${var.name}-daily-funding"
  schedule_expression          = "cron(59 * * * ? *)"
  schedule_expression_timezone = "UTC"

  flexible_time_window {
    mode = "OFF"
  }

  target {
    arn      = aws_lambda_function.api.arn
    role_arn = aws_iam_role.scheduler.arn
    # literal string: jsonencode would escape the < > of the placeholder; retries keep the same scheduled time
    input = "{\"task\":\"daily-funding\",\"scheduledTime\":\"<aws.scheduler.scheduled-time>\"}"

    retry_policy {
      maximum_retry_attempts       = 3
      maximum_event_age_in_seconds = 3600
    }
  }
}

# ponytail: URL is public (auth NONE) so POST/PUT bodies work through CloudFront without SigV4 hashing;
# direct callers are rejected by the ORIGIN_SECRET header check, then bearer tokens + login rate limit apply.
resource "aws_lambda_function_url" "api" {
  function_name      = aws_lambda_function.api.function_name
  authorization_type = "NONE"
}

# A NONE-auth URL still needs this resource policy, or every call (CloudFront's included) gets 403.
# ponytail: AWS is also moving to require lambda:InvokeFunction with invoked_via_function_url = true for public URLs;
# that argument needs aws provider 6.x (this stack is on ~> 5.0), so add it when you upgrade the provider.
resource "aws_lambda_permission" "url" {
  statement_id           = "FunctionURLAllowPublicAccess"
  action                 = "lambda:InvokeFunctionUrl"
  function_name          = aws_lambda_function.api.function_name
  principal              = "*"
  function_url_auth_type = "NONE"
}

# ---------- S3 (SPA files, private) ----------

resource "aws_s3_bucket" "spa" {
  bucket = "${var.name}-spa"
}

resource "aws_s3_bucket_public_access_block" "spa" {
  bucket                  = aws_s3_bucket.spa.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_policy" "spa" {
  bucket = aws_s3_bucket.spa.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "cloudfront.amazonaws.com" }
      Action    = "s3:GetObject"
      Resource  = "${aws_s3_bucket.spa.arn}/*"
      Condition = { StringEquals = { "AWS:SourceArn" = aws_cloudfront_distribution.main.arn } }
    }]
  })
}

# ---------- CloudFront: /* -> S3, /api/* -> Lambda ----------

resource "aws_cloudfront_origin_access_control" "spa" {
  name                              = "${var.name}-spa"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

data "aws_cloudfront_cache_policy" "optimized" { name = "Managed-CachingOptimized" }
data "aws_cloudfront_cache_policy" "disabled" { name = "Managed-CachingDisabled" }
data "aws_cloudfront_origin_request_policy" "all_but_host" { name = "Managed-AllViewerExceptHostHeader" }

# SPA client-side routing: extension-less paths serve index.html (only on the S3 behavior, so API 404s stay 404s).
resource "aws_cloudfront_function" "spa_rewrite" {
  name    = "${var.name}-spa-rewrite"
  runtime = "cloudfront-js-2.0"
  code    = <<-JS
    function handler(event) {
      var r = event.request;
      if (!r.uri.includes('.')) r.uri = '/index.html';
      return r;
    }
  JS
}

resource "aws_cloudfront_distribution" "main" {
  enabled             = true
  default_root_object = "index.html"
  price_class         = "PriceClass_100" # North America + Europe edges: cheapest

  origin {
    origin_id                = "spa"
    domain_name              = aws_s3_bucket.spa.bucket_regional_domain_name
    origin_access_control_id = aws_cloudfront_origin_access_control.spa.id
  }

  origin {
    origin_id   = "api"
    domain_name = split("/", aws_lambda_function_url.api.function_url)[2]
    custom_header {
      name  = "x-origin-secret"
      value = random_password.origin_secret.result
    }
    custom_origin_config {
      http_port              = 80
      https_port             = 443
      origin_protocol_policy = "https-only"
      origin_ssl_protocols   = ["TLSv1.2"]
    }
  }

  default_cache_behavior {
    target_origin_id       = "spa"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD"]
    cached_methods         = ["GET", "HEAD"]
    cache_policy_id        = data.aws_cloudfront_cache_policy.optimized.id
    function_association {
      event_type   = "viewer-request"
      function_arn = aws_cloudfront_function.spa_rewrite.arn
    }
  }

  ordered_cache_behavior {
    path_pattern             = "/api/*"
    target_origin_id         = "api"
    viewer_protocol_policy   = "https-only"
    allowed_methods          = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
    cached_methods           = ["GET", "HEAD"]
    cache_policy_id          = data.aws_cloudfront_cache_policy.disabled.id
    origin_request_policy_id = data.aws_cloudfront_origin_request_policy.all_but_host.id
  }

  restrictions {
    geo_restriction { restriction_type = "none" }
  }

  viewer_certificate {
    cloudfront_default_certificate = true
  }
}

# ---------- Outputs ----------

output "url" {
  value = "https://${aws_cloudfront_distribution.main.domain_name}"
}

output "spa_bucket" {
  description = "Upload the SPA build here: aws s3 sync ./dist s3://<bucket>"
  value       = aws_s3_bucket.spa.bucket
}

output "cloudfront_distribution_id" {
  description = "CloudFront distribution ID for cache invalidation"
  value       = aws_cloudfront_distribution.main.id
}

output "function_url" {
  value = aws_lambda_function_url.api.function_url
}
