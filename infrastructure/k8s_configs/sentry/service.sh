SERVICE_POD=`kubectl -n sentry get pod -l app=service | grep service | head -n1 | awk '{print $1}'`
kubectl -n sentry exec -it "$SERVICE_POD" -- /bin/bash
