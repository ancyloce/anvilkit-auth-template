# **Product Requirements Document: Universal Email Microservice & Verification Flow**

## **1\. Executive Summary**

This document defines the requirements for a **Universal Email Microservice** designed to centralize and secure all outbound communications. The service operates on an asynchronous, event-driven model to ensure that high-volume transactional emails (e.g., identity verification, password resets) are delivered with maximum reliability and security, adhering to NIST AAL2 standards.1

## ---

**2\. Business Process Flow**

This flowchart illustrates the high-level logic from user initiation through the microservice pipeline to final account activation.

Code snippet

graph TD  
    User\[User: Initiates Action\] \--\> BizService  
    BizService \--\> MQ\[Message Queue: Email Event\]  
      
    subgraph Email Microservice  
        MQ \--\> Consumer\[Event Consumer\]  
        Consumer \--\> TemplateEngine  
        TemplateEngine \--\> Routing  
    end  
      
    Routing \--\> ESP  
    ESP \--\> Inbox\[User Inbox\]  
      
    Inbox \--\> Interaction{User Interaction}  
    Interaction \-- "Clicks Magic Link" \--\> MLFlow  
    Interaction \-- "Inputs 6-Digit Code" \--\> OTPFlow  
      
    MLFlow & OTPFlow \--\> Verify  
    Verify \-- "Success" \--\> Final  
    Verify \-- "Failure/Expired" \--\> Fallback

## ---

**3\. System Sequence Diagram**

This diagram details the asynchronous lifecycle of a verification request, including the status tracking via webhooks.

Code snippet

sequenceDiagram  
    participant User as User / Client  
    participant Auth as Auth Service  
    participant MQ as Message Queue  
    participant MailSvc as Email Service  
    participant ESP as Email Provider (ESP)

    User-\>\>Auth: Request Verification  
    Auth-\>\>Auth: Generate 15m JWT / State Token  
    Auth-\>\>MQ: Publish "SEND\_EMAIL" (UserID, TemplateID, Payload)  
    Auth--\>\>User: 202 Accepted (Redirect to "Check Email")

    MQ-\>\>MailSvc: Consume Event  
    MailSvc-\>\>MailSvc: Map Data to Template  
    MailSvc-\>\>ESP: API POST: Send Email  
    ESP--\>\>MailSvc: Return External TransactionID  
      
    ESP--\>\>User: Delivery to Inbox  
      
    User-\>\>Auth: Click Magic Link (OTP \+ State)  
    Auth-\>\>Auth: Verify State matches Browser Session  
    alt Valid Context  
        Auth-\>\>User: 200 OK: Authentication Success  
        Auth-\>\>Auth: Invalidate all existing sessions  
    else Cross-Device Detected  
        Auth-\>\>User: Prompt for Manual OTP Entry  
    end  
      
    ESP-\>\>MailSvc: Webhook: Delivered/Opened/Bounced  
    MailSvc-\>\>MailSvc: Update Status History Logs

## ---

**4\. Functional Specifications**

### **4.1 Verification Mechanisms**

2

* **One-Time Passcode (OTP):** 6-digit numeric codes generated with high entropy. Standard expiration is set to **15 minutes**.4  
* **Magic Links:** Cryptographically secure, single-use URLs.  
  * **Same-Device Enforcement:** The system must match the state token in the URL with the state stored in the user's browser session. If a user clicks the link on a different device (e.g., initiating on a laptop but clicking on a phone), the system must fallback to manual OTP entry to prevent session hijacking.3

### **4.2 Rate Limiting & Anti-Abuse**

5

* **Resend Logic:** The "Resend Code" button must include a mandatory **90-second delay** between attempts to prevent automated flooding.5  
* **Attempt Limits:** Users are limited to **6 resend attempts** per session. If exceeded, they must restart the entire onboarding/login process.5  
* **Algorithm:** Use the **Token Bucket** algorithm to allow for minor bursts while maintaining a strict long-term average rate limit per IP.7

### **4.3 Security Post-Action**

8

* **Session Invalidation:** Upon a successful password reset or critical email change, the system **must automatically invalidate all existing sessions** for that user to terminate any potentially stolen tokens.9

## ---

**5\. Database & Tracking Schema**

To manage massive scale and provide clear audit trails, the service will utilize a three-tier data model:

1. **Email Job:** A top-level group for batch sends or large campaigns.  
2. **Email Record:** Individual record for every unique recipient (includes UserID, ExternalID from ESP, and metadata).  
3. **Status History:** An immutable, time-series log of every event related to a record (e.g., Queued \-\> Sent \-\> Delivered \-\> Clicked).

## ---

**6\. Infrastructure & Deliverability**

10  
Verification emails must maintain a **99%+ delivery rate** to ensure user trust.

* **Authentication Trio:** SPF, DKIM, and DMARC must be configured at the DNS level. DMARC policy should be set to p=reject for production to prevent spoofing.10  
* **IP Strategy:** Isolate high-priority transactional traffic on dedicated IPs to prevent marketing campaign bounces from affecting critical OTP delivery.12  
* **Bounce Handling:** Hard bounces (invalid addresses) must be blacklisted immediately. Soft bounces (mailbox full) should trigger a maximum of 3 retries over 24 hours.14

## ---

**7\. Metrics & Analytics (Mixpanel/Segment)**

16  
Follow the object\_action snake\_case naming convention 18:

* verification\_email\_sent  
* verification\_email\_bounced (Property: bounce\_type)  
* verification\_link\_clicked (Property: latency\_from\_sent)  
* account\_activated (Property: method: magic\_link|otp)

**KPI Targets:**

* **Activation Rate:** \>85% of registration starts should complete verification.  
* **TTV (Time to Value):** Median time from registration to verification should be \< 3 minutes.20
