# coding=utf-8
from flask import g, session, request
from flask_restplus import Namespace, abort, fields
from sqlalchemy import and_, or_, func

import stripe

from gpbilling.lib.email import send_email
from gpbilling.model import Account
from gpbilling.views.api.base import ResourceBase

ns = Namespace("account", "Account managment")

@ns.route("/")
class AccountResource(ResourceBase):
    def get(self):
        if not g.user:
            return {"error": "You are not logged in."}, 403

        return g.user.to_dict()

    @ns.param("email", "New email", _in="formData", type=str)
    @ns.param("email_verify", "New email to verify", _in="formData", type=str)
    @ns.param("password", "New password", _in="formData", type=str)
    @ns.param("new_password", "New password to verify", _in="formData", type=str)
    @ns.param("refresh_stripe", "Attempt to relink with a Stripe customer object", _in="formData", type=str)
    def put(self):
        if not g.user:
            return {"error": "You are not logged in."}, 403
        
        response = {"messages": []}
        
        refresh_stripe = self.get_field("refresh_stripe")
        if refresh_stripe:
            try:
                customer = stripe.Customer.list(email=g.user.email)["data"][0]
                print(customer)
                g.user.stripe_customer = customer["id"]
                response["messages"].append("A Stripe customer object has been linked to your account.")
            except KeyError:
                response["messages"].append("A Stripe customer object could not be found with your email.")

        # XXX: Do stuff with fields

        return response

@ns.route("/register")
class AccountRegisterResource(ResourceBase):
    @ns.param("username", "Username", type=str, _in="formData", required=True)
    @ns.param("password", "Password", type=str, _in="formData", required=True)
    @ns.param("password_verify", "Password to verify", type=str, _in="formData", required=True)
    @ns.param("email", "Email", type=str, _in="formData", required=True)
    def post(self):
        username = self.get_field("username")
        password = self.get_field("password")
        password_verify = self.get_field("password_verify")
        email = self.get_field("email")

        # Don't accept blank fields.
        if username == "" or password == "":
            return {"error": "Username or password is blank"}, 400

        # Make sure the two passwords match.
        if password != password_verify:
            return {"error": "Passwords do not match."}, 400
        
        # Check if the email is filled.
        if not email:
            return {"error": "Email is blank."}, 400

        # Make sure this email address hasn't been taken before.
        if g.db.query(Account.id).filter(
            func.lower(Account.email) == email.lower()
        ).count() != 0:
            return {"error": "Email is taken."}, 400

        # Make sure this username hasn't been taken before.
        if g.db.query(Account.id).filter(
            func.lower(Account.username) == username.lower()
        ).count() == 1:
            return {"error": "Username is taken."}, 400

        new_account = Account(
            username=username,
            email=email,
        )
        new_account.set_password(password)
        g.db.add(new_account)
        g.db.flush()

        session["user_id"] = new_account.id
        g.user = new_account
        send_email("verify", email)

        g.db.commit()

@ns.route("/login")
class AccountLoginResource(ResourceBase):
    @ns.param("username", "Username", type=str, _in="formData", required=True)
    @ns.param("password", "Password", type=str, _in="formData", required=True)
    def post(self):
        username = self.get_field("username")
        password = self.get_field("password")

        # Check username, lowercase to make it case-insensitive.
        print(username)
        user = g.db.query(Account).filter(
            func.lower(Account.username) == username.lower()
        ).scalar()
        if not user:
            return {"error": "User does not exist."}, 400

        # Check password.
        if not user.check_password(password):
            return {"error": "Incorrect password"}, 400

        session["user_id"] = user.id

        return user.to_dict()

@ns.route("/logout")
class AccountLogoutResource(ResourceBase):
    def post(self):
        logged_out = False

        if "user_id" in session:
            session.pop("user_id")
            logged_out = True

        return {
            "logged_out": logged_out
        }

@ns.route("/verify")
class AccountVerifyResource(ResourceBase):
    @ns.param("token", "Token", type=str, _in="formData", required=True)
    def post(self):
        token = self.get_field("token")
        verify_key = g.redis.get("verify:" + token)

        try:
            user_id, email = verify_key.split(":", 2)
        except ValueError:
            return {"error": "Verify token is not valid."}, 400

        user = g.db.query(Account).filter(Account.id == user_id).one()
        if user.email == email:
            user.email_verified = True

        return {"success": True}
