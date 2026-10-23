#!/bin/sh
# A throwaway service-account key for a fake Firebase: a fresh RSA key and the
# token_uri given, in the shape the Firebase console hands out. For the e2e
# sweep's fake (E2E_FCM) and nothing else: Google has never heard of it, so it
# opens nothing. Write it outside the repository:
#
#   scripts/fcm-test-key.sh http://127.0.0.1:8094/token > "$TMPDIR/gathering-fcm-test.json"
set -eu
uri=${1:?usage: fcm-test-key.sh TOKEN_URI > key.json}
pem=$(openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 2>/dev/null | awk '{ printf "%s\\n", $0 }')
printf '{"type":"service_account","project_id":"gathering-e2e","private_key_id":"e2e","private_key":"%s","client_email":"sweep@gathering-e2e.iam.gserviceaccount.com","client_id":"1","token_uri":"%s"}\n' "$pem" "$uri"
