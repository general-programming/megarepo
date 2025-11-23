{{ saltenv }}:
  '*':
    - refresh_pillar
    - common
    - groups
    - admin_user
