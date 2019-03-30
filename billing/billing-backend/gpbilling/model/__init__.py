# This Python file uses the following encoding: utf-8
import datetime
import os

import stripe
from bcrypt import gensalt, hashpw

from sqlalchemy import BigInteger, Boolean, Column, DateTime, ForeignKey, Integer, String, Unicode, create_engine
from sqlalchemy.dialects.postgresql import JSONB
from sqlalchemy.ext.declarative import declarative_base
from sqlalchemy.orm import relationship, scoped_session, sessionmaker, validates
from sqlalchemy.schema import Index

debug = os.environ.get('DEBUG', False)

engine = create_engine(os.environ["POSTGRES_URL"], convert_unicode=True, pool_recycle=3600)

if debug:
    engine.echo = True

sm = sessionmaker(autocommit=False,
                  autoflush=False,
                  bind=engine)

base_session = scoped_session(sm)

Base = declarative_base()
Base.query = base_session.query_property()


def now():
    return datetime.datetime.now()


class Account(Base):
    __tablename__ = "accounts"
    id = Column(Integer, primary_key=True)

    username = Column(String(50), nullable=False)
    password = Column(String(60), nullable=False)

    github_id = Column(BigInteger)

    email = Column(Unicode)
    email_verified = Column(Boolean, nullable=False, server_default='f', default=False)

    stripe_customer = Column(Unicode)

    def set_password(self, password):
        if not password:
            raise ValueError("Password can't be blank.")
        self.password = hashpw(password.encode("utf8"), gensalt()).decode("utf8")

    def check_password(self, password):
        return hashpw(password.encode("utf8"), self.password.encode()).decode("utf8") == self.password

    def to_dict(self):
        return {
            "id": self.id,
            "github_id": self.github_id or None,
            "email": self.email,
            "email_verified": self.email_verified,
            "stripe_linked": hasattr(self, "stripe_customer") and self.stripe_customer != "",
        }
