`./deployment/webhook-patch-ca-bundle.sh <mutatingwebhook.yaml | kubectl apply -f -` # Creates webhook
`./deployment/webhook-create-signed-cert.sh` # Creates secrets
`./build` # Builds binary and tags to kev/x:v10
Load serviceaccount
Load clusterrole
Load clusterrolebinding
Load service 
Change image in deployment to be kev/x:v10
Change ImagePullPolicy to be Never
Load deployment
`kubectl create -f deployment\sleep.yaml`

Should add `initContainer` and `volume` and `volumeMounts`
Try `kubectl exec sleep-xxx cat /secrets/secret.txt` # Should return hello
