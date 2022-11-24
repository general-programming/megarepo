terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
    }
  }
}

data "aws_region" "current" {}

resource "aws_security_group" "nlsd" {
  name = "nlsd"
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
        values = ["ubuntu/images/hvm-ssd/ubuntu-focal-20.04-arm64-server-*"]
    }

    filter {
        name = "virtualization-type"
        values = ["hvm"]
    }

    owners = ["099720109477"]
}

resource "aws_iam_role" "example" {
  name = "example-fleet-role-${data.aws_region.current.name}"
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

resource "aws_spot_fleet_request" "nlsd" {
    launch_specification {
        instance_type = "t4g.micro"
        spot_price = "0.01"
        ami = "${data.aws_ami.ubuntu.id}"
        user_data = templatefile("${path.module}/user_data.tmpl", {})

        vpc_security_group_ids = ["${aws_security_group.nlsd.id}"]
        associate_public_ip_address = true

        root_block_device {
            volume_size = 8
        }
    }

    iam_fleet_role      = "${aws_iam_role.example.arn}"
    spot_price          = "0.5"
    target_capacity     = 50
    terminate_instances_with_expiration = true
}
