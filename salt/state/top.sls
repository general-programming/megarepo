{{ saltenv }}:
  '*':
    - refresh_pillar
    - common
    - admin_user
