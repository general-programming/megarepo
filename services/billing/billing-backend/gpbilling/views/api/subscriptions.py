# coding=utf-8
import stripe
from flask import g
from flask_restplus import Namespace
from stripe.error import InvalidRequestError

from gpbilling.views.api.base import ResourceBase

ns = Namespace("subscriptions", "Subscriptions")


@ns.route("/")
class SubscriptionResource(ResourceBase):
    def sub_to_dict(self, sub):
        return sub["id"], {
            "name": sub["plan"]["nickname"],
            "price": sub["plan"]["amount"] / 100.0,
            "currency": sub["plan"]["currency"],
        }

    def get(self):
        subscriptions = {}

        if not g.user:
            return {"error": "You are not logged in."}, 403

        if g.user.stripe_customer:
            try:
                for subscription in stripe.Subscription.list(
                    customer=g.user.stripe_customer
                )["data"]:
                    if "items" in subscription:
                        for item in subscription["items"]:
                            sub_id, sub_meta = self.sub_to_dict(item)
                            subscriptions[sub_id] = sub_meta
                    else:
                        sub_id, sub_meta = self.sub_to_dict(subscription)
                        subscriptions[sub_id] = sub_meta
            except InvalidRequestError as e:
                if "No such customer" in str(e):
                    return {"error": "Customer object is invalid."}, 500
                else:
                    return {"error": str(e)}, 500

        return subscriptions

    @ns.param(
        "subscription_id", "Subscription ID", type=str, _in="formData", required=True
    )
    def delete(self):
        sub_id = self.get_field("subscription_id")

        try:
            sub = stripe.Subscription.retrieve(sub_id)
        except InvalidRequestError as e:
            return {"error": str(e)}, 400

        if sub.customer != g.user.stripe_customer:
            return {"error": "This subscription is not yours!"}, 403

        sub.delete()
        return {"deleted": True}
