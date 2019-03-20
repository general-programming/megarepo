# coding=utf-8
from flask import g
from flask_restplus import Namespace, abort, fields
from sqlalchemy import and_, or_

from gpbilling.views.api.base import ResourceBase

ns = Namespace("permissions", "Account managment")

# ResourceBase routes go here.
