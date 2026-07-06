schedule:
  highstate:
    - function: state.highstate
    - hours: 4
    - splay: 3600
  salt_minion_slowtop:
    - function: state.top
    - args:
      - slowtop.sls
    - weeks: 1
    - splay: 3600
