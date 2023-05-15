data "aws_region" "current" {}

resource "aws_security_group" "nlsd" {
  name = "${data.aws_region.current.name}-${var.nickname}"
  description = "Allow inbound traffic and all outbound."

  ingress {
      from_port = 0
      to_port = 0
      protocol = "-1"
      cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
      from_port = 0
      to_port = 0
      protocol = "-1"
      cidr_blocks = ["0.0.0.0/0"]
  }
}

data "aws_ami" "ubuntu" {

    most_recent = true

    filter {
        name   = "name"
        values = ["ubuntu/images/hvm-ssd/ubuntu-jammy-22.04-amd64-server-*"]
    }

    filter {
        name = "virtualization-type"
        values = ["hvm"]
    }

    owners = ["099720109477"]
}

resource "aws_iam_role" "example" {
  name = "required-role-${var.nickname}-${data.aws_region.current.name}"
  assume_role_policy = <<EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "",
      "Effect": "Allow",
      "Principal": {
        "Service": "spotfleet.amazonaws.com"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}
EOF
}

resource "aws_iam_role_policy_attachment" "AmazonEC2SpotFleetTaggingRole-policy-attachment" {
  role = "${aws_iam_role.example.name}"
  policy_arn = "arn:aws:iam::aws:policy/service-role/AmazonEC2SpotFleetTaggingRole"
}

resource "aws_key_pair" "nepeat" {
  key_name   = var.nickname
  public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIMVk9i7FG7dc9r4ixwAJT7uPLH3UuqbwIgeZ7Ytmnpvv erin"
}

resource "aws_spot_fleet_request" "nlsd" {
  launch_specification {
    instance_type = "t3a.micro"
    ami = "${data.aws_ami.ubuntu.id}"
    user_data = templatefile("${path.module}/user_data.tmpl", {
      concurrency = var.concurrency
      nickname = var.nickname
    })
    vpc_security_group_ids = [ "${aws_security_group.nlsd.id}" ]
    associate_public_ip_address = true
    key_name = aws_key_pair.nepeat.key_name
    root_block_device {
      volume_size = 16
    }
  }

  iam_fleet_role      = "${aws_iam_role.example.arn}"
  spot_price          = var.spot_price
  target_capacity     = var.instances
  terminate_instances_with_expiration = true
  excess_capacity_termination_policy = "NoTermination"
  allocation_strategy = "lowestPrice"
}
