import logging

logging.basicConfig(level=logging.INFO)
logging.getLogger("paramiko.transport").setLevel(logging.WARNING)
