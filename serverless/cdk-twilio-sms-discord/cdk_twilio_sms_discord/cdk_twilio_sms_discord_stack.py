from email.mime import image

import aws_cdk
import hvac
from aws_cdk import (
    Duration,
    Stack,
    aws_apigateway,
    aws_dynamodb,
    aws_lambda,
    aws_lambda_python_alpha,
)
from constructs import Construct


def get_secret(secret: str, key: str = "secret") -> str:
    """Vault secret fetcher.

    Args:
        secret (str): The secret's path.

    Returns:
        str: The secret's value.
    """
    vault = hvac.Client()
    response = vault.secrets.kv.v2.read_secret_version(path=secret,)[
        "data"
    ]["data"]
    return response[key]


class CdkTwilioSmsDiscordStack(Stack):
    def __init__(self, scope: Construct, construct_id: str, **kwargs) -> None:
        super().__init__(scope, construct_id, **kwargs)

        # Define DDB Table
        table_config = aws_dynamodb.Table(
            self,
            "AppConfig",
            partition_key={
                "name": "guild_id",
                "type": aws_dynamodb.AttributeType.NUMBER,
            },
            table_name="DiscordTwilioConfig",
            billing_mode=aws_dynamodb.BillingMode.PAY_PER_REQUEST,
            removal_policy=aws_cdk.RemovalPolicy.RETAIN,
        )

        table_messages = aws_dynamodb.Table(
            self,
            "Messages",
            partition_key={
                "name": "external_number",
                "type": aws_dynamodb.AttributeType.STRING,
            },
            sort_key={
                "name": "twilio_number",
                "type": aws_dynamodb.AttributeType.STRING,
            },
            table_name="DiscordTwilioMessages",
            billing_mode=aws_dynamodb.BillingMode.PAY_PER_REQUEST,
            removal_policy=aws_cdk.RemovalPolicy.RETAIN,
        )

        # Defines an AWS Lambda resource
        my_lambda = aws_lambda_python_alpha.PythonFunction(
            self,
            "DiscordHandler",
            runtime=aws_lambda.Runtime.PYTHON_3_9,
            entry="lambda/",
            environment={
                "DDB_TABLE_CONFIG": table_config.table_name,
                "DDB_TABLE_MESSAGES": table_messages.table_name,
                "DISCORD_PUBLIC_KEY": get_secret(
                    "twilio-discord", "discord_public_key"
                ),
            },
            timeout=Duration.seconds(10),
            index="index.py",
            architecture=aws_lambda.Architecture.ARM_64,
            bundling=aws_lambda_python_alpha.BundlingOptions(
                image=aws_cdk.DockerImage.from_build(
                    path="lambda/", platform="linux/arm64"
                )
            ),
        )

        # Grant lambda permissions to the databases.
        table_config.grant_read_write_data(my_lambda)
        table_messages.grant_read_write_data(my_lambda)

        # Defines an API Gateway resource for the lambda.
        aws_apigateway.LambdaRestApi(
            self,
            "Endpoint",
            handler=my_lambda,
        )
