# This file is just Python, with a touch of Django which means
# you can inherit and tweak settings to your hearts content.

# For Docker, the following environment variables are supported:
#  SENTRY_POSTGRES_HOST
#  SENTRY_POSTGRES_PORT
#  SENTRY_DB_NAME
#  SENTRY_DB_USER
#  SENTRY_DB_PASSWORD
#  SENTRY_RABBITMQ_HOST
#  SENTRY_RABBITMQ_USERNAME
#  SENTRY_RABBITMQ_PASSWORD
#  SENTRY_RABBITMQ_VHOST
#  SENTRY_REDIS_HOST
#  SENTRY_REDIS_PORT
#  SENTRY_REDIS_DB
#  SENTRY_MEMCACHED_HOST
#  SENTRY_MEMCACHED_PORT
#  SENTRY_FILESTORE_DIR
#  SENTRY_SERVER_EMAIL
#  SENTRY_EMAIL_HOST
#  SENTRY_EMAIL_PORT
#  SENTRY_EMAIL_USER
#  SENTRY_EMAIL_PASSWORD
#  SENTRY_EMAIL_USE_TLS
#  SENTRY_ENABLE_EMAIL_REPLIES
#  SENTRY_SMTP_HOSTNAME
#  SENTRY_MAILGUN_API_KEY
#  SENTRY_SINGLE_ORGANIZATION
#  SENTRY_SECRET_KEY
from sentry.conf.server import *  # NOQA
from sentry.utils.types import Bool

import os
import os.path

import ldap
from django_auth_ldap.config import LDAPSearch, GroupOfNamesType

CONF_ROOT = os.path.dirname(__file__)
env = os.environ.get

postgres = env('SENTRY_POSTGRES_HOST') or (env('POSTGRES_PORT_5432_TCP_ADDR') and 'postgres')
if postgres:
    DATABASES = {
        'default': {
            'ENGINE': 'sentry.db.postgres',
            'NAME': (
                env('SENTRY_DB_NAME')
                or env('POSTGRES_ENV_POSTGRES_USER')
                or 'postgres'
            ),
            'USER': (
                env('SENTRY_DB_USER')
                or env('POSTGRES_ENV_POSTGRES_USER')
                or 'postgres'
            ),
            'PASSWORD': (
                env('SENTRY_DB_PASSWORD')
                or env('POSTGRES_ENV_POSTGRES_PASSWORD')
                or ''
            ),
            'HOST': postgres,
            'PORT': (
                env('SENTRY_POSTGRES_PORT')
                or ''
            ),
            'AUTOCOMMIT': True,
            'ATOMIC_REQUESTS': False,
        },
    }

# You should not change this setting after your database has been created
# unless you have altered all schemas first
SENTRY_USE_BIG_INTS = True

# If you're expecting any kind of real traffic on Sentry, we highly recommend
# configuring the CACHES and Redis settings

###########
# General #
###########

# Instruct Sentry that this install intends to be run by a single organization
# and thus various UI optimizations should be enabled.
SENTRY_SINGLE_ORGANIZATION = Bool(env('SENTRY_SINGLE_ORGANIZATION', True))

#########
# Redis #
#########

# Generic Redis configuration used as defaults for various things including:
# Buffers, Quotas, TSDB

redis = env('SENTRY_REDIS_HOST') or (env('REDIS_PORT_6379_TCP_ADDR') and 'redis')
if not redis:
    raise Exception('Error: REDIS_PORT_6379_TCP_ADDR (or SENTRY_REDIS_HOST) is undefined, did you forget to `--link` a redis container?')

redis_port = env('SENTRY_REDIS_PORT') or '6379'
redis_db = env('SENTRY_REDIS_DB') or '0'

SENTRY_OPTIONS.update({
    'redis.clusters': {
        'default': {
            'hosts': {
                0: {
                    'host': redis,
                    'port': redis_port,
                    'db': redis_db,
                },
            },
        },
    },
})

#########
# Cache #
#########

# Sentry currently utilizes two separate mechanisms. While CACHES is not a
# requirement, it will optimize several high throughput patterns.

CACHES = {
    "default": {
        "BACKEND": "django.core.cache.backends.memcached.MemcachedCache",
        "LOCATION": ["memcached:11211"],
        "TIMEOUT": 3600,
    }
}

# A primary cache is required for things such as processing events
SENTRY_CACHE = "sentry.cache.redis.RedisCache"

DEFAULT_KAFKA_OPTIONS = {
    "bootstrap.servers": "kafka:9092",
    "message.max.bytes": 50000000,
    "socket.timeout.ms": 1000,
}

SENTRY_EVENTSTREAM = "sentry.eventstream.kafka.KafkaEventStream"
SENTRY_EVENTSTREAM_OPTIONS = {"producer_configuration": DEFAULT_KAFKA_OPTIONS}

KAFKA_CLUSTERS["default"] = DEFAULT_KAFKA_OPTIONS

#########
# Queue #
#########

# See https://docs.getsentry.com/on-premise/server/queue/ for more
# information on configuring your queue broker and workers. Sentry relies
# on a Python framework called Celery to manage queues.

rabbitmq = env('SENTRY_RABBITMQ_HOST') or (env('RABBITMQ_PORT_5672_TCP_ADDR') and 'rabbitmq')

if rabbitmq:
    BROKER_URL = (
        'amqp://' + (
            env('SENTRY_RABBITMQ_USERNAME')
            or env('RABBITMQ_ENV_RABBITMQ_DEFAULT_USER')
            or 'guest'
        ) + ':' + (
            env('SENTRY_RABBITMQ_PASSWORD')
            or env('RABBITMQ_ENV_RABBITMQ_DEFAULT_PASS')
            or 'guest'
        ) + '@' + rabbitmq + '/' + (
            env('SENTRY_RABBITMQ_VHOST')
            or env('RABBITMQ_ENV_RABBITMQ_DEFAULT_VHOST')
            or '/'
        )
    )
else:
    BROKER_URL = 'redis://' + redis + ':' + redis_port + '/' + redis_db


###############
# Rate Limits #
###############

# Rate limits apply to notification handlers and are enforced per-project
# automatically.

SENTRY_RATELIMITER = 'sentry.ratelimits.redis.RedisRateLimiter'

##################
# Update Buffers #
##################

# Buffers (combined with queueing) act as an intermediate layer between the
# database and the storage API. They will greatly improve efficiency on large
# numbers of the same events being sent to the API in a short amount of time.
# (read: if you send any kind of real data to Sentry, you should enable buffers)

SENTRY_BUFFER = 'sentry.buffer.redis.RedisBuffer'

##########
# Quotas #
##########

# Quotas allow you to rate limit individual projects or the Sentry install as
# a whole.

SENTRY_QUOTAS = 'sentry.quotas.redis.RedisQuota'

########
# TSDB #
########

# The TSDB is used for building charts as well as making things like per-rate
# alerts possible.

SENTRY_TSDB = 'sentry.tsdb.redissnuba.RedisSnubaTSDB'

#########
# SNUBA #
#########

SENTRY_SEARCH = "sentry.search.snuba.EventsDatasetSnubaSearchBackend"
SENTRY_SEARCH_OPTIONS = {}
SENTRY_TAGSTORE_OPTIONS = {}

###########
# Digests #
###########

# The digest backend powers notification summaries.

SENTRY_DIGESTS = 'sentry.digests.backends.redis.RedisBackend'

################
# File storage #
################

# Any Django storage backend is compatible with Sentry. For more solutions see
# the django-storages package: https://django-storages.readthedocs.org/en/latest/

SENTRY_OPTIONS["filestore.backend"] = 's3'
SENTRY_OPTIONS["filestore.options"] = {
    'access_key': 'SENTRY_MINIO_INTERNAL',
    'secret_key': 'MINIO_INSECURE_SECRET',
    'bucket_name': 'sentry',
    'endpoint_url': 'http://minio:9000'
}

##############
# Web Server #
##############

# If you're using a reverse SSL proxy, you should enable the X-Forwarded-Proto
# header and set `SENTRY_USE_SSL=1`

SENTRY_WEB_HOST = "0.0.0.0"
SENTRY_WEB_PORT = 9000
SENTRY_WEB_OPTIONS = {
    "http": "%s:%s" % (SENTRY_WEB_HOST, SENTRY_WEB_PORT),
    "protocol": "uwsgi",
    # This is needed to prevent https://git.io/fj7Lw
    "uwsgi-socket": None,
    "http-keepalive": True,
    "http-chunked-input": True,
    "memory-report": False,
    # 'workers': 3,  # the number of web workers
}


###############
# Mail Server #
###############


email = env('SENTRY_EMAIL_HOST') or (env('SMTP_PORT_25_TCP_ADDR') and 'smtp')
if email:
    SENTRY_OPTIONS['mail.backend'] = 'smtp'
    SENTRY_OPTIONS['mail.host'] = email
    SENTRY_OPTIONS['mail.password'] = env('SENTRY_EMAIL_PASSWORD') or ''
    SENTRY_OPTIONS['mail.username'] = env('SENTRY_EMAIL_USER') or ''
    SENTRY_OPTIONS['mail.port'] = int(env('SENTRY_EMAIL_PORT') or 25)
    SENTRY_OPTIONS['mail.use-tls'] = Bool(env('SENTRY_EMAIL_USE_TLS', False))
else:
    SENTRY_OPTIONS['mail.backend'] = 'dummy'

# The email address to send on behalf of
SENTRY_OPTIONS['mail.from'] = env('SENTRY_SERVER_EMAIL') or 'root@localhost'

# If you're using mailgun for inbound mail, set your API key and configure a
# route to forward to /api/hooks/mailgun/inbound/
SENTRY_OPTIONS['mail.mailgun-api-key'] = env('SENTRY_MAILGUN_API_KEY') or ''

# If you specify a MAILGUN_API_KEY, you definitely want EMAIL_REPLIES
if SENTRY_OPTIONS['mail.mailgun-api-key']:
    SENTRY_OPTIONS['mail.enable-replies'] = True
else:
    SENTRY_OPTIONS['mail.enable-replies'] = Bool(env('SENTRY_ENABLE_EMAIL_REPLIES', False))

if SENTRY_OPTIONS['mail.enable-replies']:
    SENTRY_OPTIONS['mail.reply-hostname'] = env('SENTRY_SMTP_HOSTNAME') or ''

# If this value ever becomes compromised, it's important to regenerate your
# SENTRY_SECRET_KEY. Changing this value will result in all current sessions
# being invalidated.
secret_key = env('SENTRY_SECRET_KEY')
if not secret_key:
    raise Exception('Error: SENTRY_SECRET_KEY is undefined, run `generate-secret-key` and set to -e SENTRY_SECRET_KEY')

if 'SENTRY_RUNNING_UWSGI' not in os.environ and len(secret_key) < 32:
    print('!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!')
    print('!!                    CAUTION                       !!')
    print('!! Your SENTRY_SECRET_KEY is potentially insecure.  !!')
    print('!!    We recommend at least 32 characters long.     !!')
    print('!!     Regenerate with `generate-secret-key`.       !!')
    print('!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!')

SENTRY_OPTIONS['system.secret-key'] = secret_key

# Auth
AUTHENTICATION_BACKENDS = (
    'sentry.utils.auth.EmailAuthBackend',
    'social_auth.backends.github.GithubBackend',
)
SENTRY_FEATURES['organizations:sso'] = True

# LDAP
AUTH_LDAP_SERVER_URI = 'ldap://ipa.generalprogramming.org'
AUTH_LDAP_BIND_DN = 'uid=sentrybind,cn=users,cn=accounts,dc=ipa,dc=generalprogramming,dc=org'
AUTH_LDAP_BIND_PASSWORD = '03681cfb-ce34-4ac6-9ded-6a011960622c'

AUTH_LDAP_USER_SEARCH = LDAPSearch(
    'cn=users,cn=accounts,dc=ipa,dc=generalprogramming,dc=org',
    ldap.SCOPE_SUBTREE,
    '(uid=%(user)s)',
)

AUTH_LDAP_GROUP_SEARCH = LDAPSearch(
    'cn=groups,cn=accounts,dc=ipa,dc=generalprogramming,dc=org',
    ldap.SCOPE_SUBTREE,
    '(objectClass=groupOfNames)'
)

AUTH_LDAP_SENTRY_GROUP_ROLE_MAPPING = {
    'owner': ['superadmins'],
    'admin': ['admins', 'superadmins'],
    'member': ['ipausers'],
}

AUTH_LDAP_GROUP_TYPE = GroupOfNamesType()
AUTH_LDAP_REQUIRE_GROUP = None
AUTH_LDAP_DENY_GROUP = None

AUTH_LDAP_USER_ATTR_MAP = {
    'name': 'cn',
    'email': 'mail'
}

AUTH_LDAP_FIND_GROUP_PERMS = True
AUTH_LDAP_CACHE_GROUPS = True
AUTH_LDAP_GROUP_CACHE_TIMEOUT = 3600

AUTH_LDAP_DEFAULT_SENTRY_ORGANIZATION = u'Sentry'
AUTH_LDAP_SENTRY_ORGANIZATION_ROLE_TYPE = 'member'
AUTH_LDAP_SENTRY_USERNAME_FIELD = 'uid'
AUTH_LDAP_SENTRY_ORGANIZATION_GLOBAL_ACCESS = True

AUTHENTICATION_BACKENDS = AUTHENTICATION_BACKENDS + (
    'sentry_ldap_auth.backend.SentryLdapBackend',
)

# Github
# GITHUB_APP_ID = "42f02d9b8a6e2aef3baf"
# GITHUB_API_SECRET = "cc45e3e064f2d1a5fa9942d840537a51f817298d"
GITHUB_EXTENDED_PERMISSIONS = ["repo"]
STATIC_URL = "/_static/"

############
# Features #
############

SENTRY_FEATURES["projects:sample-events"] = False
SENTRY_FEATURES.update(
    {
        feature: True
        for feature in (
            "organizations:discover",
            "organizations:events",
            "organizations:discover-basic",
            "organizations:discover-query",
            "organizations:events-v2",
            "organizations:global-views",
            "organizations:integrations-issue-basic",
            "organizations:integrations-issue-sync",
            "organizations:invite-members",
            "organizations:sso-basic",
            "organizations:sso-rippling",
            "organizations:sso-saml2",
            "projects:custom-inbound-filters",
            "projects:data-forwarding",
            "projects:discard-groups",
            "projects:plugins",
            "projects:rate-limits",
            "projects:servicehooks",
        )
    }
)

# Other
SENTRY_URL_PREFIX = env('SENTRY_URL_PREFIX')
