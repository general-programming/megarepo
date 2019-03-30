# coding=utf-8
from flask import g, session, request
from flask_restplus import Namespace, abort, fields
from sqlalchemy import and_, or_, func

import stripe
from stripe.error import InvalidRequestError
from gpbilling.views.api.base import ResourceBase

ns = Namespace("subscriptions", "Subscriptions")

@ns.route("/")
class SubscriptionResource(ResourceBase):
    def get(self):
        subscriptions = {}

        if not g.user:
            return {"error": "You are not logged in."}, 403

        if g.user.stripe_customer:
            try:
                for subscription in stripe.Subscription.list(customer=g.user.stripe_customer)["data"]:
                    subscriptions[subscription["id"]] = {
                        "name": subscription["plan"]["nickname"],
                        "price": subscription["plan"]["amount"] / 100.0,
                        "currency": subscription["plan"]["currency"],
                    }
            except InvalidRequestError as e:
                if "No such customer" in str(e):
                    return {"error": "Customer object is invalid."}, 500
                else:
                    return {"error": str(e)}, 500

        return subscriptions

    @ns.param("subscription_id", "Subscription ID", type=str, _in="formData", required=True)
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
