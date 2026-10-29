# OCI Functions image (push to OCIR; oci_functions_function.image points at it).
FROM fnproject/python:3.11-dev AS build
WORKDIR /function
COPY requirements.txt .
RUN pip install --no-cache-dir --target /python -r requirements.txt

FROM fnproject/python:3.11 AS runtime
WORKDIR /function
COPY --from=build /python /python
COPY src/ .
ENV PYTHONPATH=/function:/python
ENTRYPOINT ["/python/bin/fdk", "/function/func.py", "handler"]
