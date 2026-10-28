# Added by hostim deploy: serves this folder as a static site with nginx.
FROM nginx:stable-alpine
COPY . /usr/share/nginx/html
RUN mv /usr/share/nginx/html/.hostim/nginx.conf /etc/nginx/conf.d/default.conf \
 && rm -r /usr/share/nginx/html/.hostim /usr/share/nginx/html/Dockerfile
