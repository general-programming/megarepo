create_test_file:
  file.managed:
    - name: /tmp/testfile.txt
    - contents: {{ salt['pillar.get']('common:test_value') }}
