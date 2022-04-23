FROM python:3.9

COPY requirements.txt .

RUN pip install -r requirements.txt

COPY . ${LAMBDA_TASK_ROOT}

CMD [ "index.handler" ]
