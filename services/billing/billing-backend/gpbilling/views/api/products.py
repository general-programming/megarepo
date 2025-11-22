# coding=utf-8
import stripe
from flask_restplus import Namespace

from gpbilling.views.api.base import ResourceBase

ns = Namespace("products", "Products")


@ns.route("/")
class ProductResource(ResourceBase):
    def get(self):
        products = []

        for product in stripe.Plan.list()["data"]:
            if not product["active"] or product["interval"] != "month":
                continue

            products.append(
                {
                    "id": product["id"],
                    "name": product["nickname"],
                    "price": product["amount"] / 100.0,
                }
            )

        return {"products": products}
