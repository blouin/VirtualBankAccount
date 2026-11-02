function handler(event) {
  var request = event.request;
  var uri = request.uri;

  // 1. For root/entry requests, perform geolocation routing if language is not already set in query string
  var isLanding = uri === '/' || uri === '/index.html';
  var querystring = request.querystring || {};
  var hasLngQuery = querystring.lng && querystring.lng.value;

  if (isLanding && !hasLngQuery) {
    var cookies = request.cookies || {};
    var preferredLang = cookies['vbank.lang'] ? cookies['vbank.lang'].value : null;

    // If user has no saved cookie preference, check CloudFront geo headers for Quebec (CA-QC)
    if (!preferredLang) {
      var headers = request.headers || {};
      var country = headers['cloudfront-viewer-country'] ? headers['cloudfront-viewer-country'].value : '';
      var region = headers['cloudfront-viewer-country-region'] ? headers['cloudfront-viewer-country-region'].value : '';
      if (country === 'CA' && region === 'QC') {
        preferredLang = 'fr';
      }
    }

    // If a language was determined (either from cookie or Quebec IP), redirect to set URL
    if (preferredLang) {
      var params = [];
      for (var key in querystring) {
        if (Object.prototype.hasOwnProperty.call(querystring, key)) {
          if (key !== 'lng' && querystring[key].value) {
            params.push(key + '=' + querystring[key].value);
          }
        }
      }
      params.push('lng=' + preferredLang);
      var newQuery = '?' + params.join('&');

      return {
        statusCode: 302,
        statusDescription: 'Found',
        headers: {
          location: { value: '/' + newQuery }
        },
        cookies: {
          'vbank.lang': {
            value: preferredLang,
            attributes: 'Path=/; Max-Age=31536000; SameSite=Lax'
          }
        }
      };
    }
  }

  // 2. SPA client-side routing: extension-less paths serve /index.html (only on the S3 behavior, so API 404s stay 404s)
  if (!uri.includes('.')) {
    request.uri = '/index.html';
  }

  return request;
}
