import smtplib
import time
import ssl
from email.message import EmailMessage
from imapclient import IMAPClient

SMTP_HOST = "localhost"
SMTP_PORT = 2525

IMAP_HOST = "localhost"
IMAP_PORT = 1993

USERNAME = "test@mail.local"
PASSWORD = "test123"

def send_email():
    msg = EmailMessage()
    msg["From"] = USERNAME
    msg["To"] = USERNAME
    msg["Subject"] = "Test Email from script"
    msg.set_content("Hello from test script")

    with smtplib.SMTP(SMTP_HOST, SMTP_PORT) as smtp:
        smtp.ehlo()
        smtp.login(USERNAME, PASSWORD)
        smtp.send_message(msg)

    print("SMTP: email sent")


def check_imap():
    context = ssl.create_default_context()
    context.check_hostname = False
    context.verify_mode = ssl.CERT_NONE

    with IMAPClient(IMAP_HOST, port=IMAP_PORT, ssl=True, ssl_context=context) as server:
        server.use_uid = False
        server.login(USERNAME, PASSWORD)
        print("IMAP: login OK")

        server.select_folder("INBOX")

        messages = server.search(["ALL"])
        print(f"IMAP: {len(messages)} messages in inbox")

        if not messages:
            print("No messages found")
            return

        data = server.fetch(messages[-1:], ["BODY[]"])

        print("Latest message:\n")
        msg = next(iter(data.values()))
        print(msg[b"BODY[]"].decode(errors="ignore"))


if __name__ == "__main__":
    # send_email()
    # time.sleep(2)
    check_imap()
