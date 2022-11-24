provider "aws" {
    alias = "usw1"
    region = "us-west-1"
}

module "nlsd-usw1" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.usw1
  }
}

provider "aws" {
    alias  = "usw2"
    region = "us-west-2"
}

module "nlsd-usw2" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.usw2
  }
}

provider "aws" {
    alias  = "use1"
    region = "us-east-1"
}

module "nlsd-use1" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.use1
  }
}

provider "aws" {
    alias  = "use2"
    region = "us-east-2"
}

module "nlsd-use2" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.use2
  }
}

provider "aws" {
    alias  = "euw1"
    region = "eu-west-1"
}

module "nlsd-euw1" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.euw1
  }
}
