## Creates webhook
`./deployment/webhook-patch-ca-bundle.sh <mutatingwebhook.yaml | kubectl apply -f -` 
## Creates secrets
`./deployment/webhook-create-signed-cert.sh` 
## Builds binary and tags to kev/x:v10
`./build` 
1. Load serviceaccount
2. Load clusterrole
3. Load clusterrolebinding
4. Load service 
5. Change image in deployment to be kev/x:v10
6. Change ImagePullPolicy to be Never
7. Load deployment
`kubectl create -f deployment\sleep.yaml`

Should add `initContainer` and `volume` and `volumeMounts`
## Returns hello
Try `kubectl exec sleep-xxx cat /secrets/secret.txt`
