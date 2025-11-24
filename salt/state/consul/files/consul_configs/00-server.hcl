# {{ pillar['common']['salt_managed'] }}

{% if 'consulserver' in salt['grains.get']('tags', []) %}
server = true
ui = false
bootstrap_expect = {{ salt['pillar.get']('consul:bootstrap_expect', 3) }}
{% else %}
server = false
ui = false
{% endif %}
