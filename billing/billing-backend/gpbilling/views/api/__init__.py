# coding=utf-8
from flask import Blueprint, g, jsonify, request
from flask_restplus import Api

from gpbilling.views.api import account, products

blueprint = Blueprint("api", __name__)
api = Api(
    blueprint,
    version="lol",
    title="GP Billing API",
    description="API services for billing managment"
)


@api.errorhandler
def default_error_handler(error):
    """Default error handler"""
    return {
        'message': str(error)
    }, getattr(error, 'code', 500)


api.add_namespace(account.ns)
api.add_namespace(products.ns)
