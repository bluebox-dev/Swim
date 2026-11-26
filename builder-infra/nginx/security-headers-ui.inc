# The SWIM console only (not Guacamole, which ships its own inline scripts).
# script-src 'self': even if a machine name or a command output ever slipped into the page as
# markup, the browser would refuse to run it. frame-ancestors: no other site can frame the console.
add_header Content-Security-Policy "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'" always;
add_header X-Frame-Options "DENY" always;
