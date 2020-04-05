# coding=utf-8
import json

from flask import g, request
from flask_restplus import Resource, abort

from sqlalchemy.dialects.postgresql import insert
from sqlalchemy.orm.exc import NoResultFound


class ResourceBase(Resource):
    def get_field(self, field: str, default=None, asjson: bool=False):
        if request.is_json and field in request.json:
            return request.json.get(field, default)
        elif request.form and field in request.form:
            content = request.form.get(field, default)
        elif request.args and field in request.args:
            content = request.args.get(field, default)
        else:
            content = None

        if not content:
            return default

        if asjson:
            return json.loads(content)

        return content
