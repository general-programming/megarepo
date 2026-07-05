import logging

from jinja2 import Environment, PackageLoader, Undefined, make_logging_undefined

log = logging.getLogger(__name__)

jinja_env = Environment(
    loader=PackageLoader("barf"),
    autoescape=False,
    undefined=make_logging_undefined(log, base=Undefined),
    trim_blocks=True,
    lstrip_blocks=True,
    # Included sub-templates otherwise lose their final newline, which
    # glues heredoc terminators onto content in the linux file blocks.
    keep_trailing_newline=True,
)


def render_template(template_name: str, *args, **kwargs):
    template = jinja_env.get_template(template_name)
    return template.render(*args, **kwargs)
