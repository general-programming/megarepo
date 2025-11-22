# coding=utf-8
from flask import Blueprint, url_for
from flask_restplus import Api

from gpbilling.views.api import account, products, subscriptions

blueprint = Blueprint("api", __name__)


class CustomApi(Api):
    # Nasty hack to get swagger.json to be served as a relative URL.
    @property
    def specs_url(self):
        """
        The Swagger specifications absolute url (ie. `swagger.json`)

        :rtype: str
        """
        return url_for(self.endpoint("specs"), _external=False)


api = CustomApi(
    blueprint,
    version="lol",
    title="GP Billing API",
    description="API services for billing managment",
)


@api.errorhandler
def default_error_handler(error):
    """Default error handler"""
    return {"message": str(error)}, getattr(error, "code", 500)


api.add_namespace(account.ns)
api.add_namespace(products.ns)
api.add_namespace(subscriptions.ns)
