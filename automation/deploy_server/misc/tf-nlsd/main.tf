module "nlsd-usw1" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.usw1
  }

  concurrency = 3
  instances   = 8
  nickname = "nepeat"
  spot_price = "0.0095"
}

module "nlsd-usw2" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.usw2
  }

  concurrency = 3
  instances   = 8
  nickname = "nepeat"
  spot_price = "0.0095"
}

# module "nlsd-use1" {
#   source = "./mod_nlsd"
#   providers = {
#     aws = aws.use1
#   }

#   concurrency = 10
#   instances   = 5
#   nickname = "nepeat"
#   spot_price = "0.0095"
# }

module "nlsd-use2" {
  source    = "./mod_nlsd"
  providers = {
    aws = aws.use2
  }

  concurrency = 3
  instances   = 8
  nickname = "nepeat"
  spot_price = "0.0095"
}

# module "nlsd-euw1" {
#   source    = "./mod_nlsd"
#   providers = {
#     aws = aws.euw1
#   }
# }
