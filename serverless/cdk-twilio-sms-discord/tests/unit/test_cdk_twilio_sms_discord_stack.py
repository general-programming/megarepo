import aws_cdk as core
import aws_cdk.assertions as assertions

from cdk_twilio_sms_discord.cdk_twilio_sms_discord_stack import CdkTwilioSmsDiscordStack

# example tests. To run these tests, uncomment this file along with the example
# resource in cdk_twilio_sms_discord/cdk_twilio_sms_discord_stack.py
def test_sqs_queue_created():
    app = core.App()
    stack = CdkTwilioSmsDiscordStack(app, "cdk-twilio-sms-discord")
    template = assertions.Template.from_stack(stack)

#     template.has_resource_properties("AWS::SQS::Queue", {
#         "VisibilityTimeout": 300
#     })
