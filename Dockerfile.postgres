FROM postgres:16
RUN apt-get update && apt-get install -y locales \
    && echo "ru_RU.UTF-8 UTF-8" >> /etc/locale.gen \
    && locale-gen ru_RU.UTF-8
ENV LANG=ru_RU.utf8
